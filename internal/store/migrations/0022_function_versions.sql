-- Keep a bounded, immutable snapshot history. The sequence avoids revision
-- reuse after deletion/recreation; updates serialize on the live function row.
CREATE SEQUENCE cloud_function_revision_seq;
ALTER TABLE cloud_functions ADD COLUMN revision BIGINT NOT NULL DEFAULT nextval('cloud_function_revision_seq');
CREATE TABLE cloud_function_versions (
    function_id UUID NOT NULL REFERENCES cloud_functions(id) ON DELETE CASCADE,
    revision BIGINT NOT NULL,
    code TEXT NOT NULL,
    enabled BOOLEAN NOT NULL,
    public BOOLEAN NOT NULL,
    cron_secs INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (function_id, revision)
);
INSERT INTO cloud_function_versions
SELECT id, revision, code, enabled, public, cron_secs, updated_at FROM cloud_functions;

CREATE FUNCTION bump_cloud_function_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.revision := nextval('cloud_function_revision_seq');
    NEW.updated_at := now();
    RETURN NEW;
END $$;
CREATE TRIGGER cloud_function_revision BEFORE UPDATE ON cloud_functions
FOR EACH ROW EXECUTE FUNCTION bump_cloud_function_revision();

CREATE FUNCTION snapshot_cloud_function() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO cloud_function_versions(function_id, revision, code, enabled, public, cron_secs, created_at)
    VALUES (NEW.id, NEW.revision, NEW.code, NEW.enabled, NEW.public, NEW.cron_secs, NEW.updated_at);
    DELETE FROM cloud_function_versions WHERE function_id = NEW.id AND revision IN (
        SELECT revision FROM cloud_function_versions WHERE function_id = NEW.id
        ORDER BY revision DESC OFFSET 50
    );
    RETURN NEW;
END $$;
CREATE TRIGGER cloud_function_snapshot AFTER INSERT OR UPDATE ON cloud_functions
FOR EACH ROW EXECUTE FUNCTION snapshot_cloud_function();
