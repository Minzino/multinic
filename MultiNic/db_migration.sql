-- MultiNic Database Migration Script
-- 실행 날짜: $(date)
-- 목적: multi_subnet, multi_interface 테이블에 새로운 컬럼 추가

-- 1. multi_subnet 테이블에 nic_name 컬럼 추가
ALTER TABLE multi_subnet 
ADD COLUMN nic_name VARCHAR(255) NULL COMMENT 'NIC 이름';

-- 2. multi_interface 테이블에 attached_node_name 컬럼 추가
-- (attached_node_id 뒤에 추가, node_table의 attached_node_name을 참조하는 외래키)
ALTER TABLE multi_interface 
ADD COLUMN attached_node_name VARCHAR(255) NULL AFTER attached_node_id,
ADD FOREIGN KEY fk_attached_node_name (attached_node_name) REFERENCES node_table(attached_node_name);

-- 3. multi_interface 테이블에 netplan_success 컬럼 추가
-- (status 컬럼 뒤에 추가, 기본값 0, 0과 1로 사용)
ALTER TABLE multi_interface 
ADD COLUMN netplan_success TINYINT(1) NOT NULL DEFAULT 0 AFTER status COMMENT 'Netplan 적용 성공 여부 (0: 실패/미적용, 1: 성공)';

-- 마이그레이션 완료 확인을 위한 SELECT 구문
SELECT 'Migration completed successfully' AS status;

-- 변경된 테이블 구조 확인
DESCRIBE multi_subnet;
DESCRIBE multi_interface; 