-- release: v1.2.1
-- phase: expand
-- 존재하지 않는 테이블을 참조해 실행 중간에 실패한다(앞의 0003 은 이미 적용된 상태로 남는다).
ALTER TABLE inquiries ADD COLUMN reviewer_id integer REFERENCES staff(id);
