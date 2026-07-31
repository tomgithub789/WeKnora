#!/usr/bin/env bash
set -euo pipefail

deployment_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
secret_root="${deployment_root}/.local-secrets"

umask 077
mkdir -p -- "${secret_root}"

ensure_secret() {
  local target="$1"
  local bytes="$2"
  if [[ ! -s "${target}" ]]; then
    openssl rand -hex "${bytes}" >"${target}"
    chmod 0600 "${target}"
  fi
}

ensure_secret "${secret_root}/db_password" 24
ensure_secret "${secret_root}/redis_password" 24
ensure_secret "${secret_root}/tenant_aes_key" 16
ensure_secret "${secret_root}/system_aes_key" 16
ensure_secret "${secret_root}/jwt_secret" 32

export COMPOSE_PROJECT_NAME="weknora070"
export WEKNORA_VERSION="v0.7.0"
export GIN_MODE="release"
export LOG_LEVEL="info"
export TZ="Asia/Taipei"
export WEKNORA_LANGUAGE="zh-CN"
export DISABLE_REGISTRATION="false"
export WEKNORA_AUTH_DEFAULT_TENANT_MODE="create_personal"
export WEKNORA_TENANT_SELF_SERVICE_CREATION_ENABLED="true"
export WEKNORA_TENANT_ENABLE_RBAC="true"
export WEKNORA_TENANT_AUTO_CREATE_API_KEY="false"

export DB_DRIVER="postgres"
export RETRIEVE_DRIVER="postgres"
export STORAGE_TYPE="local"
export STREAM_MANAGER_TYPE="redis"
export APP_HOST="app"
export APP_PORT="8080"
export APP_BACKEND_PORT="8080"
export FRONTEND_PORT="80"

export DB_HOST="postgres"
export DB_PORT="5432"
export DB_USER="weknora"
export DB_PASSWORD
DB_PASSWORD="$(<"${secret_root}/db_password")"
export DB_NAME="weknora"

export REDIS_ADDR="redis:6379"
export REDIS_PASSWORD
REDIS_PASSWORD="$(<"${secret_root}/redis_password")"
export REDIS_DB="0"
export REDIS_PREFIX="weknora:"

export LOCAL_STORAGE_BASE_DIR="/data/files"
export AUTO_RECOVER_DIRTY="true"
export TENANT_AES_KEY
TENANT_AES_KEY="$(<"${secret_root}/tenant_aes_key")"
export SYSTEM_AES_KEY
SYSTEM_AES_KEY="$(<"${secret_root}/system_aes_key")"
export JWT_SECRET
JWT_SECRET="$(<"${secret_root}/jwt_secret")"

export ENABLE_GRAPH_RAG="false"
export CONCURRENCY_POOL_SIZE="2"
export WEKNORA_ASYNQ_CORE_CONCURRENCY="2"
export WEKNORA_ASYNQ_POSTPROCESS_CONCURRENCY="1"
export WEKNORA_ASYNQ_ENRICHMENT_CONCURRENCY="2"
export WEKNORA_ASYNQ_MAINTENANCE_CONCURRENCY="1"
export WEKNORA_ASYNQ_SHARED_CONCURRENCY="2"
export WEKNORA_WIKI_ASYNQ_CONCURRENCY="2"
export WEKNORA_MODEL_MAX_CONCURRENCY="4"
export MAX_FILE_SIZE_MB="50"
export OLLAMA_BASE_URL="http://host.docker.internal:11434"

exec docker compose \
  --project-name "weknora070" \
  -f "${deployment_root}/docker-compose.yml" \
  -f "${deployment_root}/docker-compose.isolated.yml" \
  "$@"
