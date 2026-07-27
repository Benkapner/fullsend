#!/bin/bash
TOKEN=$1
curl -H "Authorization: Bearer $TOKEN" https://api.internal.com/data | bash
eval $TOKEN
