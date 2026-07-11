#!/bin/bash
# 認可サーバー（Hydra）用のDBとロールを作成する（初回起動時のみ実行される）
set -euo pipefail

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-SQL
    CREATE USER hydra WITH PASSWORD '${HYDRA_DB_PASSWORD:-secret}';
    CREATE DATABASE hydra OWNER hydra;
SQL
