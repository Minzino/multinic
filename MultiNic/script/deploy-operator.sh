#!/bin/bash

set -e

echo "🚀 MultiNic Operator 배포를 시작합니다..."

# 색상 정의
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 1. Docker 이미지 빌드
echo -e "\n${BLUE}📦 1단계: Docker 이미지 빌드${NC}"
cd .. && nerdctl build --no-cache -t multinic:v1alpha1 . && cd script
if [ $? -eq 0 ]; then
    echo -e "${GREEN}✅ Docker 이미지 빌드 완료${NC}"
else
    echo -e "${RED}❌ Docker 이미지 빌드 실패${NC}"
    exit 1
fi

# 1.5. 이미지를 모든 노드에 배포
echo -e "\n${BLUE}🚚 1.5단계: 이미지 배포${NC}"
echo "이미지를 모든 노드에 배포..."
nerdctl save multinic:v1alpha1 -o multinic-v1alpha1.tar

NODES=(biz1 biz2 biz3)
for node in "${NODES[@]}"; do
    echo "📦 $node 노드에 이미지 전송 중..."
    scp multinic-v1alpha1.tar $node:/tmp/
    
    echo "🔧 $node 노드에 이미지 로드 중..."
    ssh $node "sudo nerdctl load -i /tmp/multinic-v1alpha1.tar && rm /tmp/multinic-v1alpha1.tar"
    
    echo "✅ $node 노드 완료"
done

echo "🗑️ 로컬 tar 파일 정리..."
rm -f multinic-v1alpha1.tar
echo -e "${GREEN}✅ 모든 노드에 이미지 배포 완료${NC}"

# 2. CRD 적용
echo -e "\n${BLUE}📋 2단계: CRD 설치${NC}"
kubectl apply -f ../config/crd/bases/
if [ $? -eq 0 ]; then
    echo -e "${GREEN}✅ CRD 설치 완료${NC}"
else
    echo -e "${RED}❌ CRD 설치 실패${NC}"
    exit 1
fi

# 3. 네임스페이스 생성
echo -e "\n${BLUE}📁 3단계: 네임스페이스 생성${NC}"
kubectl create namespace multinic-system --dry-run=client -o yaml | kubectl apply -f -
if [ $? -eq 0 ]; then
    echo -e "${GREEN}✅ 네임스페이스 생성 완료${NC}"
else
    echo -e "${RED}❌ 네임스페이스 생성 실패${NC}"
    exit 1
fi

# 4. Operator 배포
echo -e "\n${BLUE}🤖 4단계: MultiNic Operator 배포${NC}"
kubectl apply -k ../config/operator/
if [ $? -eq 0 ]; then
    echo -e "${GREEN}✅ Operator 배포 완료${NC}"
else
    echo -e "${RED}❌ Operator 배포 실패${NC}"
    exit 1
fi

# 5. Operator Pod 상태 확인
echo -e "\n${BLUE}🔍 5단계: Operator 상태 확인${NC}"
echo "Operator Pod가 Ready 상태가 될 때까지 대기중..."
kubectl wait --for=condition=Ready pod -l app=multinic-operator -n multinic-system --timeout=300s
if [ $? -eq 0 ]; then
    echo -e "${GREEN}✅ Operator가 성공적으로 실행중입니다${NC}"
else
    echo -e "${YELLOW}⚠️  Operator Pod 상태 확인 타임아웃. 수동으로 확인해주세요.${NC}"
fi

# 6. MultiNicOperator CR 생성
echo -e "\n${BLUE}📝 6단계: MultiNicOperator CR 생성${NC}"
kubectl apply -f ../config/operator/multinic-operator-cr.yaml
if [ $? -eq 0 ]; then
    echo -e "${GREEN}✅ MultiNicOperator CR 생성 완료${NC}"
else
    echo -e "${RED}❌ MultiNicOperator CR 생성 실패${NC}"
    exit 1
fi

# 7. 전체 상태 확인
echo -e "\n${BLUE}📊 7단계: 전체 시스템 상태 확인${NC}"
echo "=================================================="
echo "📋 MultiNic Operator 상태:"
kubectl get pods -n multinic-system -l app=multinic-operator
echo ""
echo "📋 MultiNicOperator CR 상태:"
kubectl get multinicoperator -n multinic-system
echo ""
echo "📋 관리되는 리소스 상태:"
kubectl get pods,svc,statefulsets,deployments -n multinic-system
echo "=================================================="

echo -e "\n${GREEN}🎉 MultiNic Operator 배포가 완료되었습니다!${NC}"
echo -e "\n${YELLOW}📖 사용법:${NC}"
echo "  • Operator 로그 확인: kubectl logs -f deployment/multinic-operator -n multinic-system"
echo "  • MultiNicOperator 상태 확인: kubectl get multinicoperator -n multinic-system"
echo "  • 설정 수정: kubectl edit multinicoperator multinic-operator-sample -n multinic-system"
echo "  • 보호된 리소스 확인: kubectl get pods,svc,deployments -l multinic.example.com/protected=true -n multinic-system"

echo -e "\n${BLUE}🔧 다음 단계:${NC}"
echo "  1. OpenStack 엔드포인트 확인 및 수정"
echo "  2. 데이터베이스 연결 테스트"
echo "  3. MultiNic 리소스 생성 테스트" 
