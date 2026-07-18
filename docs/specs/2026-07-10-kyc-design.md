# KYC no Account — Design

**Data:** 2026-07-10
**Status:** Aprovado
**Escopo:** ctech-account (Go API + ui + cdk). Pré-requisito do saque na ctech-wallet.

## Objetivo

Verificação de identidade (CPF, nome legal, data de nascimento) sem provedor pago e sem review manual. Validação
matemática do CPF no cadastro (`basic`); verificação real acontece fora deste projeto, no primeiro depósito PIX — a
wallet compara o CPF do pagador (retornado pela API do Inter) com o declarado e confirma via endpoint interno (
`verified`). Saques só para chave PIX do mesmo CPF (regra da wallet).

## Decisões

| Decisão                     | Escolha                                                                                                      |
|-----------------------------|--------------------------------------------------------------------------------------------------------------|
| Validação de identidade     | Dígito verificador no cadastro + PIX-match no 1º depósito (custo zero, sem provedor externo)                 |
| Integração wallet → account | Endpoint interno autenticado por `client_credentials` com scope restrito; account é dono único do estado KYC |
| Níveis                      | `none` → `basic` → `verified`; claim `kyc_level` no token via scope `kyc`                                    |
| Armazenamento do CPF        | Atributo plaintext (Dynamo criptografa at rest) + item `CPF_{cpf}` para unicidade (1 CPF = 1 conta)          |
| Idade                       | 18+ obrigatório para atingir `basic`; regra centralizada no account, serviços só leem `kyc_level`            |

## Fora de escopo

Consulta à Receita/Serpro, upload de documento, painel admin de correção de dados, o PIX-match em si (implementado na
wallet), fluxo de saque.

---

## A. Modelo de dados

### User (campos novos)

```go
CPF           string `dynamodbav:"cpf,omitempty"`        // 11 dígitos, só números
BirthDate     string `dynamodbav:"birth_date,omitempty"` // YYYY-MM-DD
LegalName     string `dynamodbav:"legal_name,omitempty"`      // nome como na Receita
KYCLevel      string `dynamodbav:"kyc_level,omitempty"`       // "" (none) | "basic" | "verified"
KYCVerifiedAt string `dynamodbav:"kyc_verified_at,omitempty"` // RFC3339
```

### Item de unicidade

`pk = CPF_{cpf}`, atributos `user_id`, `created_at`. Gravado na mesma `TransactWriteItems` que atualiza o user, com
`ConditionExpression: attribute_not_exists(pk)`. Falha da condição → 409 `cpf-already-registered`. Mesma tabela (
`config.DynamoTable`), padrão single-table existente.

### Domínio novo `internal/domain/kyc`

- `Repository` (interface): `SaveSubmission(ctx, userID, cpf, legalName, birthDate)` (transação user + item CPF),
  `MarkVerified(ctx, userID, verifiedAt)`, `GetUser` via `user.Repository` reutilizado onde couber.
- `Service`:
    - `Submit(ctx, userID, cpf, legalName, birthDate)` — valida dígito verificador, idade ≥ 18, estado atual (
      `verified` → recusa), grava transação → nível `basic`.
    - `Confirm(ctx, userID, cpf)` — usado pelo endpoint interno; confere CPF apresentado == CPF armazenado; `basic` →
      `verified` (idempotente se já `verified` com mesmo CPF); CPF divergente → erro.
    - `Get(ctx, userID)` — nível + dados mascarados.

Constantes: `LevelNone = ""`, `LevelBasic = "basic"`, `LevelVerified = "verified"`, `MinAge = 18`.

## B. Regras de negócio

- **basic**: CPF com dígito verificador válido (rejeita sequências repetidas tipo `111.111.111-11`), nome legal
  não-vazio, nascimento válido com idade ≥ 18 na data da submissão.
    - Menor de 18 → 422 `age-requirement-not-met`; usuário permanece `none`, nada é gravado.
- **verified**: só via `Confirm` (endpoint interno). Nunca settável pelo usuário ou pela UI.
- **Imutabilidade**: após `verified`, CPF/nascimento/nome legal são imutáveis (`Submit` → 409 `kyc-already-verified`).
  Antes de `verified`, re-submissão é permitida (com step-up) e substitui os dados — a transação remove o item
  `CPF_{cpf}` antigo e cria o novo.
- `Confirm` com CPF divergente → 409 `kyc-cpf-mismatch` + evento de auditoria `kyc.confirm_failed` (sinal de fraude:
  alguém depositou de conta com CPF diferente do declarado).

## C. Grant `client_credentials` (novo)

Necessário para a wallet chamar os endpoints internos. Não existe hoje em `handler/token.go`.

- `grant_type=client_credentials` no `POST /v1.0/token`, autenticado por `client_id` + `client_secret` (mesma
  verificação de secret dos clients confidential).
- Restrito a clients `ClientType == "confidential"` **e** `FirstParty == true`. Client público ou third-party →
  `unauthorized_client`.
- Scopes: interseção do `scope` pedido com `AllowedScopes` do client. Token emitido sem `sub` de usuário —
  `sub = client_id`, sem `session_id`, sem claims de step-up (`auth_time`/`amr`/`last_mfa_at` ausentes), TTL igual aos
  demais access tokens (900s). Sem refresh token.
- Scope novo `internal:kyc` no catálogo global, marcado como interno (não aparece no consent/picker da UI; só atribuível
  a clients first-party via seed/admin — mesmo mecanismo de proteção do `FirstParty`).

## D. Rotas

| Rota                              | Auth                                            | Ação                                                                                                              |
|-----------------------------------|-------------------------------------------------|-------------------------------------------------------------------------------------------------------------------|
| `GET /v1.0/account/kyc`           | Bearer (user)                                   | `{level, cpf_masked, legal_name, birth_date, verified_at}`; CPF mascarado `***.***.***-XX` (dois últimos dígitos) |
| `POST /v1.0/account/kyc`          | Bearer + `RequireRecentMFA`                     | body `{cpf, legal_name, birth_date}` → `basic`                                                                    |
| `POST /v1.0/internal/kyc/confirm` | Bearer client_credentials, scope `internal:kyc` | body `{user_id, cpf}` → `verified`                                                                                |
| `GET /v1.0/internal/kyc/:user_id` | idem                                            | `{level, cpf, legal_name, birth_date}` — CPF completo; wallet usa para PIX-match e validação de chave de saque    |

- Middleware novo (ou extensão do `RequireAuth`): rotas internas exigem token de client (`sub == client_id`, sem sessão)
  **e** scope `internal:kyc`. Token de usuário em rota interna → 403.
- Validação: CPF `len=11,numeric` no request; dígito verificador no service. Erros RFC 7807 (`apierror`):
  `ValidationFailed`, `cpf-already-registered` (409), `kyc-already-verified` (409), `kyc-cpf-mismatch` (409),
  `age-requirement-not-met` (422).

## E. Claims e token

- Scope `kyc` (público, consentável) no catálogo → quando presente, access token e id_token/userinfo carregam claim
  `kyc_level` (string; ausente quando `none`).
- Wallet, poker e dominó pedem scope `kyc` e leem o claim direto do JWT — zero callback ao account no hot path.
- Após `Confirm`, o nível novo aparece no próximo refresh do token (mesmo padrão do step-up — sem push). CPF, nascimento
  e nome legal **nunca** entram em token.
- `SignAccessToken` ganha o nível (lido do user no momento da emissão) — assinatura estendida, call sites de `api_key` e
  `client_credentials` passam vazio.

## F. Auditoria

Eventos novos em `internal/domain/audit/events.go`:

- `kyc.submitted` — metadata: nenhum dado pessoal (sem CPF).
- `kyc.verified` — gravado pelo `Confirm`.
- `kyc.confirm_failed` — CPF divergente no confirm; metadata `client_id` do caller.

Labels i18n correspondentes em `activity.events.*` (en + pt-BR).

## G. UI

Página `/account/identity`:

- Form: CPF (máscara `000.000.000-00`, envia só dígitos), nome legal, data de nascimento. Submissão → step-up dialog
  automático (interceptor já cobre o 403).
- Badge de nível: `none` (CTA "Verificar identidade"), `basic` ("Aguardando primeiro depósito" — texto explicando que
  verificação completa no 1º depósito PIX), `verified` (check verde + data).
- Campos travados (read-only) quando `verified`.
- Item "Identidade" no `account-nav`. i18n en/pt-BR (`identity.*`).

## H. Testes

- **Unit (`domain/kyc/service_test.go`)**: tabela de CPFs válidos/inválidos (dígito verificador, sequências repetidas),
  18+ na fronteira (aniversário hoje/amanhã), transições `none→basic→verified`, re-submissão antes de verified,
  imutabilidade após verified, `Confirm` idempotente, `Confirm` com mismatch.
- **Integração (`handler/kyc_test.go`)**: submit sem step-up → 403; submit → basic → confirm interno → verified;
  unicidade (segundo user com mesmo CPF → 409); rota interna com token de usuário → 403; client sem scope
  `internal:kyc` → 403; client_credentials com client público → `unauthorized_client`; claim `kyc_level` presente no
  token com scope `kyc`.
- Mock: `memKYCRepo` em `testhelpers_test.go` com semântica real de unicidade.

## I. Cross-project

- **ui**: página nova + nav + i18n (seção G).
- **cdk**: nenhuma mudança de infra (mesma tabela). Seed do client da wallet (confidential, first-party, scopes
  `internal:kyc`) é passo operacional, não CDK.
- **ctech-dfe**: nenhum impacto (claims aditivos).
- **ctech-wallet (futuro)**: consome `POST /internal/kyc/confirm` e `GET /internal/kyc/:user_id`; gate de saque = claim
  `kyc_level == "verified"` + `last_mfa_at` fresco.

## Riscos

- **Homônimo não é problema** (match é por CPF), mas **CPF de terceiro declarado** só é pego no depósito — aceitável:
  dinheiro só entra/sai amarrado ao CPF verificado.
- Depósito de conta PJ ou de CPF de terceiro → não confirma KYC (mismatch) — wallet decide política (rejeitar/estornar);
  fora deste escopo, mas endpoint `confirm_failed` já dá o sinal.
- Scope `internal:kyc` vazando para client third-party → mitigado: catálogo marca interno + atribuição só via seed,
  teste de integração cobre negação.
