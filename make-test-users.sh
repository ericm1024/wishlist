#!/bin/bash

for i in `seq 1 3`; do
    curl -X POST \
         -H "Content-Type: application/json" \
         -d "{\"first\": \"User$i\", \"last\": \"Last\", \"email\":\"user$i@gmail.com\", \"password\":\"user$i\"}" \
         http://localhost:8080/api/signup
done
