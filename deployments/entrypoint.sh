#!/bin/bash
set -e

echo "this script is running under current folder is $(pwd)"
echo "configurations are staged at ${CANAVERAL_RUNTIME_CONFIGS} folder"
echo "content of config.json is $(cat ${CANAVERAL_RUNTIME_CONFIGS}/config.json)"
echo "content of host.json is $(cat ${CANAVERAL_RUNTIME_CONFIGS}/host.json)"

EXPECTED_VARS=(CANAVERAL_RUNTIME_CONFIGS \
  CANAVERAL_TARGET_HOST \
  CANAVERAL_TARGET_FLEET \
  CANAVERAL_STAGE \
  CANAVERAL_PIPELINE)

# for loop that iterates over each element in arr
for VARIABLE in "${EXPECTED_VARS[@]}"
do
    echo "${VARIABLE} is ${!VARIABLE}"
done

