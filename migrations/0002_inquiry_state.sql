-- release: v1.2.0
-- phase: expand
-- status(text) 를 state(smallint) 로 옮기는 expand 단계.
-- 구버전 앱은 status 만 쓰므로, 트리거가 status 쓰기를 state 에 따라 복사한다(구버전이 새 스키마에서도 동작).
ALTER TABLE inquiries ADD COLUMN state smallint NOT NULL DEFAULT 0;
ALTER TABLE inquiries ADD COLUMN priority smallint NOT NULL DEFAULT 0;

UPDATE inquiries SET state = CASE status WHEN 'answered' THEN 1 WHEN 'closed' THEN 2 ELSE 0 END;

CREATE FUNCTION inquiries_sync_state() RETURNS trigger AS $$
BEGIN
    NEW.state := CASE NEW.status WHEN 'answered' THEN 1 WHEN 'closed' THEN 2 ELSE 0 END;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER inquiries_sync_state BEFORE INSERT OR UPDATE OF status ON inquiries
    FOR EACH ROW EXECUTE FUNCTION inquiries_sync_state();
