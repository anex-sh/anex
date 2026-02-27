#!/bin/bash

cd "$(dirname "$0")"
cd docs

hugo --minify
aws s3 sync public/ s3://gpu-provider-docs/ --delete
