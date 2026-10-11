-- Opaque revisions prevent stale device saves and delete/recreate ABA conflicts.
-- Every existing row receives its own revision without changing its value.
ALTER TABLE player_data ADD COLUMN revision UUID NOT NULL DEFAULT gen_random_uuid();

-- Keep revisions correct for older app instances and administrative SQL too.
CREATE FUNCTION refresh_player_data_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.revision := gen_random_uuid();
    RETURN NEW;
END;
$$;
CREATE TRIGGER player_data_revision_on_update
    BEFORE UPDATE ON player_data FOR EACH ROW
    EXECUTE FUNCTION refresh_player_data_revision();
