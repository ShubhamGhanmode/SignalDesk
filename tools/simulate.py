#!/usr/bin/env python3
"""Generate synthetic security activity through the public ingestion contract."""
from __future__ import annotations

import argparse
from datetime import datetime, timedelta, timezone
import json
import os
import random
import sys
import time
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen
from uuid import uuid4, uuid5


def stamp(value: datetime) -> str:
    return value.isoformat(timespec="milliseconds").replace("+00:00", "Z")


def scenario(namespace, now):
    stages = [
        ("identity", "suspicious_login", "high", "sso.acme.example", "Login from an unfamiliar device and location", {"country": "IE", "device": "unrecognized"}),
        ("cloud", "privilege_escalation", "critical", "iam:analytics-role", "Elevated policy attached to analytics role", {"policy": "synthetic-admin-policy"}),
        ("endpoint", "suspicious_process", "high", "workstation-042", "Shell process launched with an encoded command", {"process": "synthetic-shell", "parent": "office-app"}),
        ("cloud", "sensitive_object_access", "critical", "s3://synthetic-finance/reports", "Unusual read volume against sensitive report objects", {"object_count": "48", "classification": "synthetic-sensitive"}),
    ]
    return [{"event_id": str(uuid5(namespace, f"scenario:{index}")), "schema_version": 1,
             "occurred_at": stamp(now - timedelta(minutes=10 - index * 2)), "domain": domain,
             "action": action, "severity": severity, "entity_id": "alex.morgan", "actor": "alex.morgan@acme.example",
             "asset": asset, "message": message, "metadata": metadata}
            for index, (domain, action, severity, asset, message, metadata) in enumerate(stages)]


def background(namespace, now, count, seed):
    rng = random.Random(seed)
    activities = [
        ("identity", "login_success", "sso.acme.example", "Successful account sign-in"),
        ("identity", "mfa_verified", "sso.acme.example", "Multi-factor challenge completed"),
        ("endpoint", "process_started", "workstation", "Signed application started"),
        ("endpoint", "agent_heartbeat", "workstation", "Endpoint health heartbeat received"),
        ("cloud", "object_read", "s3://synthetic-assets/public", "Application read a public object"),
        ("cloud", "role_assumed", "iam:read-only", "Read-only application role assumed"),
    ]
    people = ["sam.chen", "riley.patel", "jamie.kelly", "taylor.smith", "casey.ryan", "alex.morgan"]
    for index in range(count):
        domain, action, asset, message = rng.choice(activities)
        person = rng.choice(people)
        yield {"event_id": str(uuid5(namespace, f"background:{index}")), "schema_version": 1,
               "occurred_at": stamp(now - timedelta(seconds=rng.randint(0, 3500))), "domain": domain,
               "action": action, "severity": rng.choices(["info", "low", "medium"], weights=[75, 20, 5])[0],
               "entity_id": person, "actor": person + "@acme.example", "asset": asset,
               "message": message, "metadata": {"source": "signaldesk-simulator", "synthetic": "true"}}


def publish(url, key, events):
    # Construct once: HTTP retries preserve event IDs, payloads and occurrence times.
    payload = json.dumps({"events": events}).encode()
    request = Request(url.rstrip("/") + "/api/v1/events", data=payload,
                      headers={"Authorization": "Bearer " + key, "Content-Type": "application/json"}, method="POST")
    for attempt in range(5):
        try:
            with urlopen(request, timeout=20) as response:
                return json.load(response)["accepted"]
        except HTTPError as exc:
            if exc.code not in (429, 502, 503, 504) or attempt == 4:
                raise RuntimeError(f"ingestion HTTP {exc.code}: {exc.read(2048).decode(errors='replace')}") from exc
            delay = min(60, int(exc.headers.get("Retry-After", 2 ** attempt)))
        except (URLError, TimeoutError) as exc:
            if attempt == 4:
                raise RuntimeError("ingestion endpoint unavailable") from exc
            delay = 2 ** attempt
        time.sleep(delay)
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", default=os.getenv("SIGNALDESK_URL", "http://localhost:8080"))
    parser.add_argument("--api-key", default=os.getenv("SIGNALDESK_API_KEY", os.getenv("DEMO_ACME_API_KEY", "")))
    parser.add_argument("--events", type=int, default=250, help="number of background events")
    parser.add_argument("--rate", type=float, default=20, help="background events per second")
    parser.add_argument("--scenario", action="store_true", help="include the connected alex.morgan scenario")
    parser.add_argument("--seed", type=int, default=42)
    args = parser.parse_args()
    if not args.api_key or args.events < 0 or args.rate <= 0:
        parser.error("provide an ingestion key, nonnegative event count and positive rate")
    now, namespace, accepted = datetime.now(timezone.utc), uuid4(), 0
    if args.scenario:
        accepted += publish(args.url, args.api_key, scenario(namespace, now))
    batch = []
    for event in background(namespace, now, args.events, args.seed):
        batch.append(event)
        if len(batch) == 100:
            accepted += publish(args.url, args.api_key, batch)
            time.sleep(len(batch) / args.rate)
            batch = []
    if batch:
        accepted += publish(args.url, args.api_key, batch)
    print(json.dumps({"accepted": accepted, "scenario_entity": "alex.morgan" if args.scenario else None, "run_id": str(namespace)}))


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, KeyboardInterrupt) as exc:
        print(str(exc), file=sys.stderr)
        sys.exit(1)
