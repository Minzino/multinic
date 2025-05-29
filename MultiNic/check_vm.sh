#!/bin/bash

# OpenStack 인증 정보
AUTH_URL="https://121.141.64.224:15000/v3"
USERNAME="admin"
PASSWORD="cloud1234"
PROJECT_ID="ed4ec88f4f05431ea67d711f620d3a97"
DOMAIN_NAME="default"

# 토큰 얻기
echo "Getting token..."
TOKEN=$(curl -s -i -k -H "Content-Type: application/json" \
    -d '{
        "auth": {
            "identity": {
                "methods": ["password"],
                "password": {
                    "user": {
                        "name": "'$USERNAME'",
                        "domain": { "name": "'$DOMAIN_NAME'" },
                        "password": "'$PASSWORD'"
                    }
                }
            },
            "scope": {
                "project": {
                    "id": "'$PROJECT_ID'"
                }
            }
        }
    }' \
    ${AUTH_URL}/auth/tokens | grep -i "x-subject-token" | awk '{print $2}' | tr -d '\r')

echo "Token: $TOKEN"

# Nova API를 사용하여 VM 목록 조회
echo -e "\nListing VMs..."
curl -s -k -H "X-Auth-Token: $TOKEN" \
     -H "Content-Type: application/json" \
     "https://121.141.64.224:18774/v2.1/servers/detail" | python3 -m json.tool

# Neutron API를 사용하여 네트워크 목록 조회
echo -e "\nListing Networks..."
curl -s -k -H "X-Auth-Token: $TOKEN" \
     -H "Content-Type: application/json" \
     "https://121.141.64.224:19696/v2.0/networks" | python3 -m json.tool

# Neutron API를 사용하여 서브넷 목록 조회
echo -e "\nListing Subnets..."
curl -s -k -H "X-Auth-Token: $TOKEN" \
     -H "Content-Type: application/json" \
     "https://121.141.64.224:19696/v2.0/subnets" | python3 -m json.tool 