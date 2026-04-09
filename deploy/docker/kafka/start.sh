#! /bin/bash
export LANG=zh_CN.UTF-8

script_dir=$(cd "$(dirname "$0")"; pwd)

function start(){
  docker compose -f docker-compose.yml up --remove-orphans -d
}

start
