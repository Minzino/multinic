#!/bin/bash

set -e

echo "🧹 MultiNic 시스템 정리 시작..."

# 1. Controller 삭제
echo "🎮 Controller 삭제..."
cd .. && make undeploy || true
cd script

# 2. MariaDB 삭제
echo "🗄️ MariaDB 삭제..."
kubectl delete -f ../config/database/mariadb.yaml || true

# 3. PVC 삭제 (데이터도 함께 삭제됨)
echo "💾 PVC 삭제..."
kubectl delete pvc mariadb-pvc -n multinic-system || true
kubectl delete pvc mysql-storage-mariadb-0 -n multinic-system || true

# 4. CRD 삭제
echo "🔧 CRD 삭제..."
cd .. && make uninstall || true
cd script

# 5. 네임스페이스 삭제
echo "📁 네임스페이스 삭제..."
kubectl delete namespace multinic-system || true

echo "✅ 정리 완료!"
echo ""
echo "📊 남은 리소스 확인:"
kubectl get all -n multinic-system 2>/dev/null || echo "네임스페이스가 삭제되었습니다." 