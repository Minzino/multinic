#!/bin/bash

echo "🚀 모든 노드에 MultiNic 이미지 배포 시작..."

NODES=(biz1 biz2 biz3)
IMAGE_NAME="multinic:v1alpha1"
IMAGE_FILE="multinic-v1alpha1.tar"

# 이미지를 tar 파일로 저장
echo "💾 이미지를 tar 파일로 저장 중..."
nerdctl save -o $IMAGE_FILE $IMAGE_NAME

for node in "${NODES[@]}"; do
    echo "📦 $node 노드에 이미지 전송 중..."
    scp $IMAGE_FILE $node:/tmp/
    
    echo "🔧 $node 노드에 이미지 로드 중..."
    ssh $node "sudo nerdctl load -i /tmp/$IMAGE_FILE && rm /tmp/$IMAGE_FILE"
    
    echo "✅ $node 노드 완료"
done

echo "🗑️ 로컬 tar 파일 정리..."
rm $IMAGE_FILE

echo "✅ 모든 노드에 이미지 배포 완료!" 