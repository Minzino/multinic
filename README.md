# MultiNic - Kubernetes Network Controller

![MultiNic Logo](https://img.shields.io/badge/MultiNic-Network%20Controller-blue?style=for-the-badge)
![Go Version](https://img.shields.io/badge/Go-1.24+-blue.svg)
![Kubernetes](https://img.shields.io/badge/Kubernetes-v1.19+-green.svg)
![License](https://img.shields.io/badge/License-Apache%202.0-green.svg)

MultiNic은 **OpenStack 네트워크와 Kubernetes 클러스터를 통합**하는 고성능 네트워크 컨트롤러입니다. Operator Pattern을 활용하여 멀티 네트워크 인터페이스를 안전하고 자동화된 방식으로 관리합니다.

## 🚀 주요 특징

### 🔗 **OpenStack 완전 통합**
- OpenStack Nova(컴퓨트) 및 Neutron(네트워크) API와 완전 통합
- 실시간 VM 상태 동기화 및 네트워크 인터페이스 관리
- 다중 서브넷 및 네트워크 정책 지원

### 🛡️ **Operator 기반 보호 시스템**
- ValidatingWebhook을 통한 시스템 무결성 보호
- RBAC 기반 권한 분리 및 최소 권한 원칙 적용
- 자동 복구 및 생명주기 관리

### 📊 **고성능 및 관찰성**
- 연결 풀링, HTTP 클라이언트 최적화
- Prometheus 메트릭 및 실시간 모니터링
- 헬스체크 및 상태 추적

### 🗄️ **데이터 영속성**
- MariaDB를 통한 네트워크 상태 영속화
- 암호화된 인증 정보 저장
- 자동 백업 및 복구 지원

## 🏗️ 시스템 아키텍처

```
┌─────────────────────────────────────────────────────────┐
│                Kubernetes Cluster                       │
│  ┌─────────────────────────────────────────────────────┐│
│  │            multinic-system Namespace               ││
│  │                                                     ││
│  │  ┌─────────────────────────────────────────────────┐ ││
│  │  │            MultiNic Operator                   │ ││
│  │  │         (시스템 관리자)                         │ ││
│  │  │  • Controller와 DB 생명주기 관리               │ ││
│  │  │  • 보호 정책 적용                              │ ││
│  │  │  • 자동 복구 기능                              │ ││
│  │  └──┬──────────────────────────────────────────────┘ ││
│  │     │ manages & protects                             ││
│  │     ▼                                                ││
│  │  ┌─────────────────┐    ┌─────────────────────────┐ ││
│  │  │ MultiNic        │    │      MariaDB            │ ││
│  │  │ Controller      │◄──►│   (Protected)           │ ││
│  │  │ (Protected)     │    │   • StatefulSet         │ ││
│  │  │  • CRD 처리     │    │   • 영속 스토리지        │ ││
│  │  │  • OpenStack 연동│    │   • 자동 백업           │ ││
│  │  │  • 메트릭 수집   │    │                         │ ││
│  │  └─────────────────┘    └─────────────────────────┘ ││
│  └─────────────────────────────────────────────────────┘│
└───────────────────┬─────────────────────────────────────┘
                    │
                    ▼
┌─────────────────────────────────────────────────────────┐
│                  OpenStack                              │
│  ┌─────────────────┐  ┌─────────────────┐              │
│  │     Nova        │  │    Neutron      │              │
│  │   (Compute)     │  │   (Network)     │              │
│  │  • VM 관리      │  │  • 서브넷 관리   │              │
│  │  • 인터페이스   │  │  • 네트워크 정책 │              │
│  └─────────────────┘  └─────────────────┘              │
└─────────────────────────────────────────────────────────┘
```

## 🚀 빠른 시작

### 📋 요구사항

- **Kubernetes**: v1.19+
- **Go**: 1.24+
- **OpenStack**: Nova + Neutron API 접근
- **MariaDB**: 10.3+ (내부 배포 또는 외부)

### 🛠️ 1단계: Operator 배포

```bash
# 저장소 클론
git clone https://github.com/xormsdhkdwk/multinic.git
cd multinic

# Operator 자동 배포 (권장)
./MultiNic/deploy-operator.sh

# 또는 수동 배포
make -C MultiNic install
make -C MultiNic deploy IMG=multinic:v1alpha1
```

### ⚙️ 2단계: 시스템 구성

```bash
# MultiNicOperator CR을 통한 시스템 설정
kubectl apply -f - <<EOF
apiVersion: multinic.example.com/v1alpha1
kind: MultiNicOperator
metadata:
  name: production-system
  namespace: multinic-system
spec:
  controller:
    image: multinic:v1alpha1
    replicas: 2
  
  database:
    enabled: true
    storageSize: "20Gi"
  
  openstack:
    identityEndpoint: "http://110.0.0.101:5000/v3"
    networkEndpoint: "http://110.0.0.101:9696/v2.0"
    computeEndpoint: "http://110.0.0.101:8774/v2.1"
  
  protection:
    enableMutationPrevention: true
    enableAutoRecovery: true
EOF
```

### 🧪 3단계: OpenStack 구성 테스트

```bash
# OpenstackConfig 리소스 생성
kubectl apply -f - <<EOF
apiVersion: multinic.example.com/v1alpha1
kind: OpenstackConfig
metadata:
  name: test-config
spec:
  vmName: "test-vm"
  subnetName: "k8s-subnet"
  credentials:
    authURL: "http://110.0.0.101:5000"
    username: "admin"
    password: "admin"
    projectID: "your-project-id"
    domainName: "Default"
EOF
```

### 📊 4단계: 상태 확인

```bash
# 시스템 상태 확인
kubectl get multinicoperator -n multinic-system
kubectl get pods -n multinic-system

# 메트릭 확인
curl http://localhost:8082/metrics

# 로그 확인
kubectl logs -f deployment/multinic-controller -n multinic-system
```

## 📚 상세 문서

| 문서 | 설명 |
|------|------|
| [**상세 README**](MultiNic/README.md) | 전체 기능, API 레퍼런스, 고급 설정 |
| [**아키텍처 가이드**](MultiNic/docs/architecture.md) | 시스템 설계 및 구조 설명 |
| [**배포 가이드**](MultiNic/docs/deployment.md) | 프로덕션 배포 모범 사례 |
| [**개발자 가이드**](MultiNic/docs/development.md) | 로컬 개발 환경 설정 |

## 🔧 관리 명령어

```bash
# 시스템 상태 확인
kubectl get multinicoperator -n multinic-system

# 설정 변경
kubectl edit multinicoperator production-system -n multinic-system

# 로그 확인
kubectl logs -f deployment/multinic-operator -n multinic-system

# 시스템 정리
./MultiNic/cleanup-operator.sh
```

## 🛡️ 보안 특징

- **🔐 RBAC**: 역할 기반 접근 제어 및 최소 권한 원칙
- **🛡️ ValidatingWebhook**: 시스템 무결성 보호
- **🔒 암호화**: 데이터베이스 인증 정보 암호화
- **🚫 네트워크 정책**: Pod 간 통신 제한

## 📊 모니터링 및 관찰성

### Prometheus 메트릭
- `multinic_operator_reconcile_total`: Operator reconcile 횟수
- `multinic_openstack_request_duration_seconds`: OpenStack API 지연시간
- `multinic_database_operation_duration_seconds`: DB 작업 지연시간
- `multinic_active_db_connections`: 활성 DB 연결 수

### 헬스체크 엔드포인트
- `/healthz`: 전체 시스템 상태
- `/readyz`: 준비 상태 확인
- `/metrics`: Prometheus 메트릭

## 🤝 기여하기

1. **Fork** 이 저장소
2. **Feature 브랜치** 생성 (`git checkout -b feature/amazing-feature`)
3. **커밋** (`git commit -m 'Add amazing feature'`)
4. **푸시** (`git push origin feature/amazing-feature`)
5. **Pull Request** 열기

## 📄 라이선스

이 프로젝트는 **Apache License 2.0** 하에 배포됩니다. 자세한 내용은 [LICENSE](LICENSE) 파일을 참조하세요.

## 📞 지원 및 커뮤니티

- 🐛 **버그 리포트**: [GitHub Issues](https://github.com/xormsdhkdwk/multinic/issues)
- 💡 **기능 요청**: [GitHub Discussions](https://github.com/xormsdhkdwk/multinic/discussions)
- 📧 **문의**: support@multinic.example.com

---

**🎯 MultiNic으로 Kubernetes와 OpenStack 간의 완벽한 네트워크 통합을 경험해보세요!**

