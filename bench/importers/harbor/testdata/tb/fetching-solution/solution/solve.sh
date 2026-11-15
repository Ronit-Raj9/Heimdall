#!/bin/bash
apt-get update
apt-get install -y jq
jq . /app/in.json > /app/out.json
