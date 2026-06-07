# py-dfe-api — Roadmap de Migração para ctech-account

> Documento de referência persistente para a migração de autenticação.
> Criado: 2026-06-07

---

## Estado Atual (AS-IS)

### `prod_users` (DynamoDB)
```
pk            STRING   "USER_{uuid}"  — PK
email         STRING   GSI: email-index
username      STRING   GSI: username-index
hashed_password STRING  Argon2id
first_name    STRING
last_name     STRING
email_verified BOOL
is_enabled    BOOL
last_login_at STRING
sid           STRING   last session ID (JWT claim)
organizations LIST     [{pk, role, permissions}]  ← contexto py-dfe, FICA AQUI
created_at    STRING
updated_at    STRING
```

### Auth no py-dfe-api
- `POST /v1.0/auth/token` → login local → JWT HS256 24h (sem refresh token)
- `POST /v1.0/auth/register` → cria usuário
- `GET /v1.0/auth/me` → retorna user + orgs
- `PUT /v1.0/auth/profile` → atualiza nome
- `PUT /v1.0/auth/change-password`
- JWT verificado em `app/core/security.py` com `SECRET_KEY` (SSM)
- `get_current_user_id` → dependência FastAPI em todas as rotas protegidas

---

## Estado Final (TO-BE)

### `prod_users` (DynamoDB) — apenas membership
```
pk            STRING   "USER_{ctech_user_id}"  — PK alinhado com ctech
organizations LIST     [{pk, role, permissions}]
joined_at     STRING   (rename de created_at)
updated_at    STRING
```

### Auth no py-dfe-api
- Não há mais endpoints de auth locais
- JWT RS256 emitido pelo ctech-account, verificado usando JWKS público
- `get_current_user_id` → lê `sub` claim do JWT RS256
- `GET /v1.0/auth/me` → mantido como read-only, lê organizações do user
- py-dfe-client usa OAuth 2.0 redirect para accounts.arturocarvalho.com

---

## Fases

---

### FASE 0 — Dual-Auth (sem impacto nos usuários)

**Pré-condição:** ctech-account está deployado e funcionando.

**Mudanças em `py-dfe-api`:**

1. **`app/core/security.py`** — adicionar verificação RS256 em paralelo:
   ```python
   async def get_current_user_id(token: str = Depends(oauth2_scheme)) -> str:
       # Tenta HS256 local primeiro (backward compat)
       try:
           payload = verify_hs256(token, SECRET_KEY)
           return payload["sub"]
       except JWTError:
           pass
       # Fallback: verifica RS256 do ctech-account
       try:
           payload = await verify_rs256_oidc(token, CTECH_JWKS_URL)
           return payload["sub"]
       except JWTError:
           raise UnauthorizedProblem("Token inválido")
   ```

2. **Nova env var:** `CTECH_JWKS_URL=https://accounts.arturocarvalho.com/.well-known/jwks.json`

3. **Cache das chaves JWKS** no Valkey (TTL 1h) para não bater no ctech-account em cada request.

4. Adicionar `ctech_user_id` como campo opcional na tabela `users` (sem quebrar nada).

**Testes necessários:**
- [ ] Token HS256 existente continua funcionando
- [ ] Token RS256 do ctech-account também funciona
- [ ] Token inválido → 401

**Rollback:** remover o bloco RS256 de `security.py`.

---

### FASE 1 — Migração de Dados (sem downtime)

**Script one-shot** (rodar em homologação primeiro):

```python
# migrate_users_to_ctech.py
# Para cada user em prod_users:
# 1. Cria usuário no ctech-account via API interna (POST /internal/users/migrate)
#    com mesmo email + password_hash (Argon2id é compatível)
# 2. Recebe ctech_user_id
# 3. Atualiza prod_users.ctech_user_id = ctech_user_id
# 4. Envia email: "Atualizamos nossa autenticação"
```

**Endpoint interno no ctech-account** (`POST /internal/v1.0/users/migrate`):
- Autenticado via shared secret (header `X-Internal-Token`)
- Aceita `{email, password_hash, first_name, last_name}`
- Cria user com `email_verified=true` (já eram verificados no py-dfe)
- Idempotente: se email já existe, retorna o user_id existente

**Considerações:**
- O PK em `prod_users` é `USER_{py-dfe-uuid}`. Após migração, o ctech emite seu próprio UUID.
- O campo `ctech_user_id` é adicionado como atributo extra — o PK ainda é o UUID antigo nesta fase.
- A troca de PK acontece na Fase 4.

**Checklist:**
- [ ] Implementar endpoint `/internal/v1.0/users/migrate` no ctech-account
- [ ] Implementar e testar script de migração em dev
- [ ] Rodar em staging com todos os usuários de staging
- [ ] Confirmar que todos os usuários têm `ctech_user_id` preenchido
- [ ] Rodar em prod (em horário de baixo tráfego)

---

### FASE 2 — py-dfe-client Switch (coordenado)

**py-dfe-client:**
- Remover formulário de login local (`/login`)
- Implementar redirect OAuth 2.0:
  ```typescript
  // lib/auth/oauth.ts
  export function redirectToLogin() {
    const params = new URLSearchParams({
      client_id: 'pydfe',
      redirect_uri: `${window.location.origin}/callback`,
      response_type: 'code',
      scope: 'openid profile email',
      state: generateState(),
      code_challenge: await generateCodeChallenge(),
      code_challenge_method: 'S256',
    });
    window.location.href = `https://accounts.arturocarvalho.com/v1.0/authorize?${params}`;
  }
  ```
- Implementar `/callback` page: troca `code` → tokens via `POST /v1.0/token`
- `access_token` em memória (não localStorage)
- `refresh_token` chega via httpOnly cookie setado pelo ctech-account
- Silent refresh: antes de request com token expirado → POST ao ctech `/v1.0/token?grant_type=refresh_token`

**py-dfe-api (nesta fase):**
- Dual-auth ainda ativo (Fase 0 permanece)
- Sem mudanças de código nesta fase

**Deploy:**
- Deploy simultâneo do py-dfe-client (novo) + garantir que ctech-account está estável
- Monitorar: taxa de 401s, login errors no CloudWatch

**Checklist:**
- [ ] py-dfe-client: implementar redirect OAuth
- [ ] py-dfe-client: implementar `/callback` com PKCE
- [ ] py-dfe-client: implementar silent refresh
- [ ] py-dfe-client: remover `localStorage.setItem('token', ...)` (CLAUDE.md constraint)
- [ ] Registrar `pydfe` como OAuth client no ctech-account
- [ ] Testar fluxo completo em staging (login → callback → API call → refresh → logout)

---

### FASE 3 — Cutover Completo

**Pré-condição:** 100% das sessões ativas estão usando RS256 (verificar no CloudWatch — métrica de tokens HS256 usados = 0 por 48h).

**py-dfe-api:**
- Remover suporte HS256 de `security.py` (apenas RS256 daqui em diante)
- Remover endpoints:
  - `POST /v1.0/auth/token`
  - `POST /v1.0/auth/register`
  - `PUT /v1.0/auth/profile` (agora em contas.arturocarvalho.com)
  - `PUT /v1.0/auth/change-password`
- Manter (read-only):
  - `GET /v1.0/auth/me` — retorna apenas organizações do user (dados de perfil vêm do ctech via userinfo)
- Remover:
  - `app/services/auth.py` — `AuthService` completo
  - `app/repositories/users.py` — métodos `get_by_email`, `get_by_username`, `create_user`
  - `app/core/security.py` — `hash_password`, `verify_password`, `create_access_token` (manter só `get_current_user_id` com RS256)
  - `app/dependencies/` — remover factory `AuthService`
- Remover env var `SECRET_KEY` do py-dfe-api (manter apenas `CTECH_JWKS_URL`)
- Atualizar tests

**Checklist:**
- [ ] Confirmar que nenhuma sessão HS256 ativa existe (monitorar por 48h)
- [ ] Remover código auth local do py-dfe-api
- [ ] Atualizar `tests/unit/test_auth_service.py` → deletar
- [ ] Atualizar `tests/integration/test_auth.py` → adaptar para OAuth flow
- [ ] Remover `SECRET_KEY` do SSM de prod (após confirmação)
- [ ] Deploy + smoke test

---

### FASE 4 — Limpeza da Tabela `users`

**Pré-condição:** Fase 3 estável por 2+ semanas.

**Objetivo:** Trocar o PK de `USER_{py-dfe-uuid}` para `USER_{ctech_user_id}` e remover campos de identidade.

**Processo (sem downtime):**
1. Script de migração:
   - Para cada item em `prod_users`:
     - Lê `ctech_user_id`
     - Cria novo item com PK = `USER_{ctech_user_id}` e apenas `organizations`, `joined_at`, `updated_at`
     - Deleta item antigo
2. Remover GSIs `email-index` e `username-index` da tabela
3. Remover campos `email`, `username`, `hashed_password`, `first_name`, `last_name`, `email_verified`, `is_enabled`, `last_login_at`, `sid`, `ctech_user_id` (agora é o PK)

**py-dfe-api após Fase 4:**
- `UserRepository.build_pk(user_id)` usa diretamente o `sub` do JWT (= ctech_user_id)
- Sem mais `get_by_email`, `get_by_username`
- A tabela `users` passa a ser conceitualmente a tabela de `memberships`

**Checklist:**
- [ ] Confirmar que `ctech_user_id` está preenchido para 100% dos registros
- [ ] Rodar script de migração em dev → staging → prod
- [ ] Remover GSIs obsoletos da tabela
- [ ] Atualizar `UserRepository` para usar PK = ctech_user_id diretamente
- [ ] Remover `UserService` métodos de identidade
- [ ] Atualizar DynamoDB-Tables.md
- [ ] Atualizar OVERVIEW.md

---

## Impacto por Componente

| Componente | Fase 0 | Fase 1 | Fase 2 | Fase 3 | Fase 4 |
|---|---|---|---|---|---|
| py-dfe-api `security.py` | Adiciona RS256 | — | — | Remove HS256 | — |
| py-dfe-api `auth.py` | — | — | — | Deletar | — |
| py-dfe-api `users.py` | — | Adiciona `ctech_user_id` | — | Remove métodos auth | Remove campos identidade |
| py-dfe-client | — | — | OAuth redirect | — | — |
| DynamoDB `prod_users` | — | Adiciona campo | — | — | Troca PK + remove campos |
| SSM `SECRET_KEY` | Mantém | Mantém | Mantém | Remove | — |
| SSM `CTECH_JWKS_URL` | Adiciona | Mantém | Mantém | Mantém | Mantém |

---

## Riscos e Mitigações

| Risco | Probabilidade | Mitigação |
|---|---|---|
| Usuário já logado perde sessão no cutover | Média | Dual-auth na Fase 0 garante que tokens antigos continuam válidos até expirar (24h) |
| Divergência de password_hash entre py-dfe e ctech | Baixa | Argon2id é compatível — mesma biblioteca em ambos |
| Performance do JWKS fetch | Média | Cache das chaves no Valkey (TTL 1h), background refresh |
| Falha no script de migração (Fase 1) | Baixa | Script é idempotente, pode ser reexecutado; dual-auth garante continuidade |
| Usuários com email duplicado (edge case) | Muito baixa | Script verifica duplicatas antes de criar |
| `prod_users` PK incompatível após Fase 4 | Baixa | Rodar em dev/staging primeiro, ter rollback script |

---

## Notas Técnicas

### JWKS Cache no py-dfe-api

```python
# app/core/security.py
_JWKS_CACHE: dict = {}
_JWKS_CACHE_TTL = 3600

async def get_jwks() -> dict:
    now = time.time()
    if _JWKS_CACHE.get("expires_at", 0) > now:
        return _JWKS_CACHE["keys"]
    async with aiohttp.ClientSession() as session:
        async with session.get(CTECH_JWKS_URL) as resp:
            data = await resp.json()
    _JWKS_CACHE["keys"] = data["keys"]
    _JWKS_CACHE["expires_at"] = now + _JWKS_CACHE_TTL
    return _JWKS_CACHE["keys"]
```

Ou via Valkey se disponível (melhor para múltiplas réplicas).

### O `GET /v1.0/auth/me` após Fase 3

Mantido como conveniência, mas apenas retorna as organizações do usuário (sem dados de perfil — esses vêm do ctech via `/v1.0/userinfo`). O py-dfe-client pode combinar os dois:

```typescript
const [me, profile] = await Promise.all([
  api.get('/v1.0/auth/me'),
  ctechApi.get('/v1.0/userinfo'),
]);
```

### Registro do py-dfe como OAuth Client

```bash
# Via ctech-account API (após first deploy)
curl -X POST https://accounts.arturocarvalho.com/v1.0/account/oauth-clients \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "py-dfe",
    "client_type": "public",
    "redirect_uris": [
      "https://pydfe.arturocarvalho.com/callback",
      "https://pydfe-dev.arturocarvalho.com/callback"
    ],
    "allowed_scopes": ["openid", "profile", "email"]
  }'
```

Guardar o `client_id` retornado como env var/SSM no py-dfe-client.
