#!/bin/bash

set -e

echo "🗄️ MultiNic 데이터베이스 마이그레이션을 시작합니다..."

# 색상 정의
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 데이터베이스 설정
DB_HOST=${DB_HOST:-"mariadb.multinic-system.svc.cluster.local"}
DB_PORT=${DB_PORT:-"3306"}
DB_USER=${DB_USER:-"root"}
DB_PASSWORD=${DB_PASSWORD:-"cloud1234"}
DB_NAME=${DB_NAME:-"multinic"}

echo -e "\n${BLUE}📋 데이터베이스 연결 정보:${NC}"
echo "  • Host: $DB_HOST"
echo "  • Port: $DB_PORT"
echo "  • Database: $DB_NAME"
echo "  • User: $DB_USER"

# 1. 데이터베이스 연결 테스트
echo -e "\n${BLUE}🔗 1단계: 데이터베이스 연결 테스트${NC}"
if kubectl exec -n multinic-system $(kubectl get pods -n multinic-system -l app=mariadb -o jsonpath='{.items[0].metadata.name}') -- mysql -h$DB_HOST -P$DB_PORT -u$DB_USER -p$DB_PASSWORD -e "SELECT 1;" &>/dev/null; then
    echo -e "${GREEN}✅ 데이터베이스 연결 성공${NC}"
else
    echo -e "${RED}❌ 데이터베이스 연결 실패${NC}"
    echo -e "${YELLOW}💡 MariaDB Pod가 실행 중인지 확인해주세요: kubectl get pods -n multinic-system -l app=mariadb${NC}"
    exit 1
fi

# 2. 백업 생성
echo -e "\n${BLUE}💾 2단계: 데이터베이스 백업 생성${NC}"
BACKUP_FILE="multinic_backup_$(date +%Y%m%d_%H%M%S).sql"
kubectl exec -n multinic-system $(kubectl get pods -n multinic-system -l app=mariadb -o jsonpath='{.items[0].metadata.name}') -- mysqldump -h$DB_HOST -P$DB_PORT -u$DB_USER -p$DB_PASSWORD $DB_NAME > $BACKUP_FILE
if [ $? -eq 0 ]; then
    echo -e "${GREEN}✅ 백업 생성 완료: $BACKUP_FILE${NC}"
else
    echo -e "${RED}❌ 백업 생성 실패${NC}"
    exit 1
fi

# 3. 현재 테이블 구조 확인
echo -e "\n${BLUE}🔍 3단계: 현재 테이블 구조 확인${NC}"
echo "multi_subnet 테이블:"
kubectl exec -n multinic-system $(kubectl get pods -n multinic-system -l app=mariadb -o jsonpath='{.items[0].metadata.name}') -- mysql -h$DB_HOST -P$DB_PORT -u$DB_USER -p$DB_PASSWORD $DB_NAME -e "DESCRIBE multi_subnet;"

echo -e "\nmulti_interface 테이블:"
kubectl exec -n multinic-system $(kubectl get pods -n multinic-system -l app=mariadb -o jsonpath='{.items[0].metadata.name}') -- mysql -h$DB_HOST -P$DB_PORT -u$DB_USER -p$DB_PASSWORD $DB_NAME -e "DESCRIBE multi_interface;"

# 4. 마이그레이션 실행
echo -e "\n${BLUE}🔧 4단계: 스키마 마이그레이션 실행${NC}"
kubectl exec -n multinic-system $(kubectl get pods -n multinic-system -l app=mariadb -o jsonpath='{.items[0].metadata.name}') -- mysql -h$DB_HOST -P$DB_PORT -u$DB_USER -p$DB_PASSWORD $DB_NAME < db_migration.sql
if [ $? -eq 0 ]; then
    echo -e "${GREEN}✅ 마이그레이션 실행 완료${NC}"
else
    echo -e "${RED}❌ 마이그레이션 실행 실패${NC}"
    echo -e "${YELLOW}🔙 백업에서 복원하려면: kubectl exec -n multinic-system POD_NAME -- mysql -h$DB_HOST -P$DB_PORT -u$DB_USER -p$DB_PASSWORD $DB_NAME < $BACKUP_FILE${NC}"
    exit 1
fi

# 5. 변경된 테이블 구조 확인
echo -e "\n${BLUE}✅ 5단계: 변경된 테이블 구조 확인${NC}"
echo "업데이트된 multi_subnet 테이블:"
kubectl exec -n multinic-system $(kubectl get pods -n multinic-system -l app=mariadb -o jsonpath='{.items[0].metadata.name}') -- mysql -h$DB_HOST -P$DB_PORT -u$DB_USER -p$DB_PASSWORD $DB_NAME -e "DESCRIBE multi_subnet;"

echo -e "\n업데이트된 multi_interface 테이블:"
kubectl exec -n multinic-system $(kubectl get pods -n multinic-system -l app=mariadb -o jsonpath='{.items[0].metadata.name}') -- mysql -h$DB_HOST -P$DB_PORT -u$DB_USER -p$DB_PASSWORD $DB_NAME -e "DESCRIBE multi_interface;"

echo -e "\n${GREEN}🎉 데이터베이스 마이그레이션이 완료되었습니다!${NC}"
echo -e "\n${YELLOW}📝 변경사항 요약:${NC}"
echo "  • multi_subnet 테이블에 nic_name 컬럼 추가 (VARCHAR(255) NULL)"
echo "  • multi_interface 테이블에 attached_node_name 컬럼 추가 (VARCHAR(255) NULL, 외래키)"
echo "  • multi_interface 테이블에 netplan_success 컬럼 추가 (TINYINT(1) DEFAULT 0)"
echo -e "\n${BLUE}💾 백업 파일: $BACKUP_FILE${NC}" 