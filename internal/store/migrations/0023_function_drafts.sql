-- One shared draft per function, with optimistic revision checks.
CREATE TABLE cloud_function_drafts (
 function_id UUID PRIMARY KEY REFERENCES cloud_functions(id) ON DELETE CASCADE,
 code TEXT NOT NULL,
 enabled BOOLEAN NOT NULL,
 public BOOLEAN NOT NULL,
 cron_secs INT NOT NULL,
 revision BIGINT NOT NULL DEFAULT nextval('cloud_function_revision_seq'),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
