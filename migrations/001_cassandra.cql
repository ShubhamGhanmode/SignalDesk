CREATE TABLE IF NOT EXISTS events_by_entity (
    tenant_id text,
    entity_id text,
    event_day date,
    occurred_at timestamp,
    event_id uuid,
    payload text,
    PRIMARY KEY ((tenant_id,entity_id,event_day),occurred_at,event_id)
) WITH CLUSTERING ORDER BY (occurred_at DESC,event_id ASC)
AND compaction = {'class':'TimeWindowCompactionStrategy','compaction_window_unit':'DAYS','compaction_window_size':'1'};
