#!/bin/bash

set -e

echo "🧹 MultiNic Operator 정리를 시작합니다..."

# 색상 정의
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 1. MultiNicOperator CR 삭제
echo -e "\n${BLUE}📝 1단계: MultiNicOperator CR 삭제${NC}"
kubectl delete multinicoperator --all -n multinic-system --ignore-not-found=true
if [ $? -eq 0 ]; then
    echo -e "${GREEN}✅ MultiNicOperator CR 삭제 완료${NC}"
else
    echo -e "${YELLOW}⚠️  MultiNicOperator CR 삭제 중 오류 발생${NC}"
fi

# 2. Operator 배포 삭제
echo -e "\n${BLUE}🤖 2단계: MultiNic Operator 삭제${NC}"
kubectl delete -k config/operator/ --ignore-not-found=true
if [ $? -eq 0 ]; then
    echo -e "${GREEN}✅ Operator 삭제 완료${NC}"
else
    echo -e "${YELLOW}⚠️  Operator 삭제 중 오류 발생${NC}"
fi

# 3. 보호된 리소스 강제 삭제 (protection label 제거 후)
echo -e "\n${BLUE}🛡️ 3단계: 보호된 리소스 정리${NC}"
echo "보호 라벨을 제거하고 리소스를 삭제합니다..."

# Protection 라벨 제거
kubectl patch deployment multinic-controller -n multinic-system --type='merge' -p='{"metadata":{"labels":{"multinic.example.com/protected":null}}}' --ignore-not-found=true
kubectl patch statefulset mariadb -n multinic-system --type='merge' -p='{"metadata":{"labels":{"multinic.example.com/protected":null}}}' --ignore-not-found=true
kubectl patch service mariadb -n multinic-system --type='merge' -p='{"metadata":{"labels":{"multinic.example.com/protected":null}}}' --ignore-not-found=true

# 강제 삭제
kubectl delete deployment multinic-controller -n multinic-system --ignore-not-found=true
kubectl delete statefulset mariadb -n multinic-system --ignore-not-found=true
kubectl delete service mariadb -n multinic-system --ignore-not-found=true
kubectl delete pvc mysql-storage-mariadb-0 -n multinic-system --ignore-not-found=true

echo -e "${GREEN}✅ 보호된 리소스 정리 완료${NC}"

# 4. CRD 삭제
echo -e "\n${BLUE}📋 4단계: CRD 삭제${NC}"
kubectl delete -f config/crd/bases/ --ignore-not-found=true
if [ $? -eq 0 ]; then
    echo -e "${GREEN}✅ CRD 삭제 완료${NC}"
else
    echo -e "${YELLOW}⚠️  CRD 삭제 중 오류 발생${NC}"
fi

# 5. 네임스페이스 정리 (선택사항)
echo -e "\n${BLUE}📁 5단계: 네임스페이스 정리${NC}"
read -p "multinic-system 네임스페이스를 삭제하시겠습니까? (y/N): " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    kubectl delete namespace multinic-system --ignore-not-found=true
    echo -e "${GREEN}✅ 네임스페이스 삭제 완료${NC}"
else
    echo -e "${YELLOW}⚠️  네임스페이스는 유지됩니다${NC}"
fi

# 6. Docker 이미지 정리 (선택사항)
echo -e "\n${BLUE}🐳 6단계: Docker 이미지 정리${NC}"
read -p "MultiNic Docker 이미지를 삭제하시겠습니까? (y/N): " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo "🗑️ MultiNic Docker 이미지 삭제 중..."
    nerdctl rmi multinic:v1alpha1 --force 2>/dev/null || true
    echo -e "${GREEN}✅ Docker 이미지 삭제 완료${NC}"
else
    echo -e "${YELLOW}⚠️  Docker 이미지는 유지됩니다${NC}"
fi

# 7. 남은 리소스 확인
echo -e "\n${BLUE}🔍 7단계: 정리 상태 확인${NC}"
echo "=================================================="
echo "📋 남은 MultiNic 관련 리소스:"
kubectl get all -n multinic-system 2>/dev/null || echo "multinic-system 네임스페이스가 삭제되었습니다."
echo ""
echo "📋 남은 CRD:"
kubectl get crd | grep multinic || echo "MultiNic CRD가 모두 삭제되었습니다."
echo "=================================================="

echo -e "\n${GREEN}🎉 MultiNic Operator 정리가 완료되었습니다!${NC}"
echo -e "\n${YELLOW}📝 참고사항:${NC}"
echo "  • 일부 리소스는 finalizer로 인해 삭제가 지연될 수 있습니다"
echo "  • PVC는 데이터 보호를 위해 수동으로 삭제해야 할 수 있습니다"
echo "  • 완전한 정리를 위해 다음 명령어를 실행할 수 있습니다:"
echo "    kubectl get pvc -A | grep multinic"
echo "    kubectl get crd | grep multinic" 