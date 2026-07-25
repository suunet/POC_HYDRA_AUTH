# auth-service ローカル開発用アセット

## JWT署名鍵（RS256・Q-2 / NFR-02）

`jwt_private_dev.pem` は auth-service がアクセストークン（INF-03）を RS256 で署名するための開発用RSA秘密鍵。
**git未管理**（`.gitignore` 登録）。compose が `JWT_PRIVATE_KEY_PATH` でマウント注入する。

初回セットアップ・鍵が無い場合は以下で生成する（コンテナ内・ホストのopenssl不要）:

```sh
docker compose exec auth-service sh -c 'openssl genrsa 2048' > .docker/local/auth/jwt_private_dev.pem
```

本番の鍵配置・公開鍵配布（JWKS等）はスコープ外（app-service検証チケットで扱う）。
