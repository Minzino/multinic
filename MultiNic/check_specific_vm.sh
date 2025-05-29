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

# test-tg VM 정보만 필터링하여 조회
echo -e "\nSearching for test-tg VM..."
curl -s -k -H "X-Auth-Token: $TOKEN" \
     -H "Content-Type: application/json" \
     "https://121.141.64.224:18774/v2.1/servers/detail" | python3 -m json.tool | grep -A 50 -B 2 '"name": "test-tg"' 