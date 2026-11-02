# Operations

## Local stack

```sh
cp .env.example .env
docker compose up --build -d
docker compose --profile demo run --rm simulator
```

Compose creates persistent volumes, initializes SQL/CQL/search schemas and Kafka topics, and seeds two organizations. Initialization may wait for Cassandra and OpenSearch startup. The console is served on `http://localhost:8080`; database and worker ports stay inside the Compose network.

The seed command adds missing accounts and keys. It does not reset existing passwords. Configure `DEMO_PASSWORD` and the distinct organization keys before the first initialization. Use a URI-safe `POSTGRES_PASSWORD` because it is interpolated into a connection URL.

### Simulator

```sh
docker compose --profile demo run --rm simulator python /tools/simulate.py --url http://api:8080 --scenario --events 1000 --rate 40
```

To populate Northstar instead of Acme, override `SIGNALDESK_API_KEY` on the simulator container with the Northstar key from `.env`. Each run creates fresh event IDs. HTTP retries within a run preserve the IDs and payloads.

The dependency-free native client also accepts:

```sh
python tools/simulate.py --url http://localhost:8080 --api-key YOUR_KEY --scenario --events 250
```

## Service commands

| Command | Purpose |
| --- | --- |
| `signaldesk init` | Create schema, search index and topics |
| `signaldesk init --seed` | Initialize and add demo organizations/accounts from environment |
| `signaldesk api` | Serve console and API |
| `signaldesk worker timeline` | Project Kafka events to Cassandra |
| `signaldesk worker search` | Project Kafka events to OpenSearch |

Environment configuration is implemented in `internal/config/config.go`. Native development requires reachable external services and the connection variables shown in `.env.example`. Compose service hostnames are internal to the Compose network.

## Optional Bedrock

Set `BEDROCK_MODEL_ID` to a Converse-compatible model or inference profile available to the account, along with `AWS_REGION`. Local Compose reads AWS credentials from `.env`. The credential needs `bedrock:InvokeModel` access to the model and any referenced inference resources. In EKS, use an IAM role associated with the brief service account. The model has no tools; its output is parsed and its event references are checked. Failed or invalid output uses the deterministic evidence brief.

## Kubernetes

Install `kubectl`, use a cluster with a default storage class, and publish both application images to a registry accessible by its nodes. Set the two image names and tags in `deploy/k8s/kustomization.yaml`.

```sh
kubectl apply -f deploy/k8s/namespace.yaml
kubectl create secret generic signaldesk-secrets --namespace signaldesk --from-env-file=.env
kubectl apply -k deploy/k8s
kubectl port-forward --namespace signaldesk service/api 8080:8080
```

The supplied manifests use persistent single-node dependency services and separate API/worker Deployments. The initialization Job retries while dependencies start. For a later schema initialization, delete the completed `signaldesk-init` Job and apply the manifests again.

For a browser-facing HTTPS endpoint, configure your ingress, set `PUBLIC_URL` to that origin and `COOKIE_SECURE=true` in the application ConfigMap, and restart the API Deployment. Keep the database and brief services internal.

The brief Deployment uses the `signaldesk-brief` service account. Configure its IAM role and `BEDROCK_MODEL_ID` in the ConfigMap to enable Bedrock on EKS. Only the brief service requires model permissions.

## AWS EKS

Requirements: AWS CLI, `eksctl`, `kubectl`, Docker, and AWS access to create EKS, IAM, ECR, networking and EBS resources. The cluster configuration uses `eu-west-1` and two `m6i.large` worker nodes.

```sh
eksctl create cluster -f deploy/aws/cluster.yaml
kubectl apply -f deploy/aws/storageclass.yaml
```

Create ECR repositories named `signaldesk` and `signaldesk-brief`. Authenticate Docker to the account's ECR registry. Build and publish the images using your registry URI:

```sh
docker build -t YOUR_ECR_REGISTRY/signaldesk:v1 .
docker build -f services/brief/Dockerfile -t YOUR_ECR_REGISTRY/signaldesk-brief:v1 .
docker push YOUR_ECR_REGISTRY/signaldesk:v1
docker push YOUR_ECR_REGISTRY/signaldesk-brief:v1
```

Set `newName` to each full ECR repository URI and `newTag` to `v1` in the Kubernetes kustomization. Then follow the Kubernetes startup commands above. EBS CSI supplies persistent volumes through the provided GP3 storage class. The default access path is local port forwarding.

## Scaling and recovery

Scale the API independently from each projection. The event topic has six partitions by default, so up to six workers per consumer group can process partitions concurrently:

```sh
kubectl scale deployment/api --replicas=3 --namespace signaldesk
kubectl scale deployment/search-worker --replicas=3 --namespace signaldesk
```

Stop and resume a local projection while leaving ingestion active:

```sh
docker compose stop search-worker
docker compose --profile demo run --rm simulator
docker compose start search-worker
```

The restarted consumer resumes committed offsets. Replayed records use deterministic projection keys. To rebuild the search projection from retained Kafka events, stop its consumers, reset only the search group's offsets, then restart:

```sh
docker compose stop search-worker
docker compose exec kafka /opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server kafka:9092 --group signaldesk-search-v1 --topic security.events.v1 --reset-offsets --to-earliest --execute
docker compose start search-worker
```

Use the same sequence with `signaldesk-timeline-v1` for timeline replay. Cassandra TTL remains anchored to each message's original ingestion timestamp. Kafka retains messages for 30 days; workers skip records beyond that retention window.

## Observability and triage

`/metrics` exposes request/error counters, cumulative HTTP duration, ingestion/processing counts, projection retries, dead letters and last successful processing time. Kubernetes pods carry scrape annotations. Logs are JSON; each HTTP request has an `X-Request-ID`.

```sh
docker compose logs -f api timeline-worker search-worker
docker compose exec kafka /opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server kafka:9092 --all-groups --describe
```

| Symptom | First place to inspect |
| --- | --- |
| Ingestion returns 401 | Organization API key and initialized key hashes |
| Ingestion returns 429 | Organization event budget; wait for `Retry-After` |
| Ingestion/login returns 503 | Redis or Postgres; ingestion also requires Kafka |
| Search lags behind the timeline | Search consumer lag and OpenSearch write errors |
| Timeline lags behind search | Timeline consumer lag and Cassandra errors |
| Worker retries continuously | Dependency availability, schema and credentials |
| Events are dead-lettered | Topic `security.events.dead.v1`; envelope contains original bytes, error and source offset |
| Bedrock falls back | Brief service logs, model access, region and structured response |

## Stop and remove

```sh
docker compose down
```

Compose preserves data volumes. To discard the local dataset, use `docker compose down --volumes`. For AWS teardown, delete the `signaldesk` namespace and its PVCs while the EBS CSI driver is still available, then run `eksctl delete cluster -f deploy/aws/cluster.yaml`. Remove the ECR images/repositories if they are no longer needed.
