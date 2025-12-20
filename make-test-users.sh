#!/bin/bash

set -eu
set -o pipefail

for i in `seq 1 3`; do
    invite_code=$(grpcurl -plaintext unix:///$(pwd)/wishlist_admin.sock admin.WishlistAdmin.GenerateInviteCode | jq -r .code)
    curl -X POST \
         -H "Content-Type: application/json" \
         -d "{\"first\": \"User$i\", \"last\": \"Last\", \"email\":\"user$i@gmail.com\", \"password\":\"user$i\", \"invite_code\":\"$invite_code\"}" \
         http://localhost:8080/api/signup
done
