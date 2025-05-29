#!/bin/bash

set -e

echo "🚀 MultiNic with MariaDB 배포 시작..."

# 1. Docker 이미지 빌드 (선택사항)
echo "📦 Docker 이미지 빌드..."
make docker-build IMG=multinic:v1alpha1 CONTAINER_TOOL=nerdctl

# 1.5. 이미지를 모든 노드에 배포
echo "🚚 이미지를 모든 노드에 배포..."
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
echo "✅ 모든 노드에 이미지 배포 완료!"

# 2. CRD 설치
echo "🔧 CRD 설치..."
make install

# 3. 네임스페이스 생성
echo "📁 네임스페이스 생성..."
kubectl create namespace multinic-system --dry-run=client -o yaml | kubectl apply -f -

# 4. MariaDB 먼저 배포
echo "🗄️ MariaDB 배포..."
kubectl apply -f config/database/mariadb.yaml

# 5. MariaDB가 준비될 때까지 대기
echo "⏳ MariaDB 준비 상태 확인..."
kubectl wait --for=condition=ready pod -l app=mariadb -n multinic-system --timeout=300s

# 6. Controller 배포
echo "🎮 Controller 배포..."
make deploy IMG=multinic:v1alpha1

# 7. Controller가 준비될 때까지 대기
echo "⏳ Controller 준비 상태 확인..."
kubectl wait --for=condition=available deployment/multinic-controller-manager -n multinic-system --timeout=300s

echo "✅ 배포 완료!"
echo ""
echo "📊 상태 확인:"
kubectl get pods -n multinic-system
echo ""
echo "🔗 서비스 확인:"
kubectl get svc -n multinic-system
echo ""
echo "💡 로그 확인 명령어:"
echo "kubectl logs -f deployment/multinic-controller-manager -n multinic-system"
echo ""
echo "🗄️ MariaDB 접속 명령어:"
echo "kubectl exec -it statefulset/mariadb -n multinic-system -- mysql -u root -pcloud1234 multinic" 