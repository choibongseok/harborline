-- release: v2.0.0
-- phase: breaking
-- status 를 바로 지운다. v1.2.0 앱은 status 를 쓰므로 앱만 이전 버전으로 되돌리면 동작하지 않는다.
DROP TRIGGER inquiries_sync_state ON inquiries;
DROP FUNCTION inquiries_sync_state();
ALTER TABLE inquiries DROP COLUMN status;
