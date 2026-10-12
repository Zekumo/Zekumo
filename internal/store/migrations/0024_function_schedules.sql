CREATE TABLE cloud_function_schedules (
 function_id UUID PRIMARY KEY REFERENCES cloud_functions(id) ON DELETE CASCADE,
 next_run_at TIMESTAMPTZ NOT NULL,
 last_dispatched_at TIMESTAMPTZ,
 dispatch_count BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX cloud_function_schedules_due ON cloud_function_schedules(next_run_at);
INSERT INTO cloud_function_schedules(function_id,next_run_at)
SELECT id,now()+make_interval(secs=>cron_secs) FROM cloud_functions WHERE enabled AND cron_secs>0;

CREATE FUNCTION sync_cloud_function_schedule() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT NEW.enabled OR NEW.cron_secs=0 THEN
  DELETE FROM cloud_function_schedules WHERE function_id=NEW.id;
 ELSIF TG_OP='INSERT' THEN
  INSERT INTO cloud_function_schedules(function_id,next_run_at)
  VALUES(NEW.id,now()+make_interval(secs=>NEW.cron_secs));
 ELSIF NOT OLD.enabled OR OLD.cron_secs<>NEW.cron_secs THEN
  INSERT INTO cloud_function_schedules(function_id,next_run_at)
  VALUES(NEW.id,now()+make_interval(secs=>NEW.cron_secs))
  ON CONFLICT(function_id) DO UPDATE SET next_run_at=EXCLUDED.next_run_at;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER cloud_function_schedule AFTER INSERT OR UPDATE ON cloud_functions
FOR EACH ROW EXECUTE FUNCTION sync_cloud_function_schedule();
