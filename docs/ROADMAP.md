# Roadmap — CTech Platform (Account / Wallet / Games)

Ordem definida por dependência. Cada item segue o ciclo spec (`docs/specs/`) → plano (`docs/plans/`) → implementação.

## Status

| # | Item                                                  | Status                                            | Spec                                           | Plano                                                                                                    |
|---|-------------------------------------------------------|---------------------------------------------------|------------------------------------------------|----------------------------------------------------------------------------------------------------------|
| 0 | Account hardening (audit log, step-up, JWKS rotation) | **Feito** (2026-07-10, merged em main)            | `specs/2026-07-10-account-hardening-design.md` | `plans/2026-07-10-audit-log.md`, `plans/2026-07-10-step-up-auth.md`, `plans/2026-07-10-jwks-rotation.md` |
| 1 | KYC no account                                        | **Feito** (2026-07-10, branch `feat/kyc`)         | `specs/2026-07-10-kyc-design.md`               | `plans/2026-07-10-kyc.md`                                                                                |
| 2 | ctech-wallet                                          | Spec pronta                                       | `../ctech-wallet/docs/specs/2026-07-10-wallet-design.md` | —                                                                                                        |
| 3 | Assinaturas/billing                                   | Não iniciado                                      | —                                              | —                                                                                                        |
| 4 | Integração poker/dominó                               | Não iniciado                                      | —                                              | —                                                                                                        |

## 1. KYC no account (feito — 2026-07-10)

Implementado conforme spec/plano (7 tasks, TDD):

- CPF (dígito verificador) + nome legal + nascimento → nível `basic`; 18+ obrigatório.
- Verificação real (`verified`) no 1º depósito PIX: wallet compara CPF do pagador com o declarado e confirma via
  `POST /v1.0/internal/kyc/confirm`; leitura completa em `GET /v1.0/internal/kyc/:user_id`.
- Claim `kyc_level` no token via scope `kyc`; grant `client_credentials` (confidential + first-party) para chamadas
  internas (scope `internal:kyc`, invisível no catálogo público e não-atribuível via self-service).
- 1 CPF = 1 conta (item de unicidade `CPF_{cpf}`, transação condicional); imutável após verified.
- UI `/account/identity` + i18n; `cmd/seedscopes` criado (antes só documentado).

**Deploy:** re-rodar `go run ./cmd/seedscopes` por ambiente; seed do client M2M da wallet quando ela nascer.

## 2. ctech-wallet (serviço novo)

- **Ledger append-only**: saldo derivado das entradas, nunca sobrescrito; transações idempotentes (idempotency key).
- **Lock por wallet**: uma transação por vez (Valkey `SetNX` ou lock condicional DynamoDB — decidir no spec).
- **Depósito PIX sem gateway**: conta PJ Inter; QR estático com ID de transação → polling da API de consulta (
  recebimento por chave é grátis; API de cobrança é paga — evitada).
- **PIX-match**: CPF do pagador (retornado pela consulta) vs CPF declarado no account → chama
  `POST /internal/kyc/confirm`. Depósito com CPF divergente: rejeitar/estornar (política a definir no spec).
- **Saque PIX**: API da conta PJ; gate = `kyc_level == "verified"` + step-up (`last_mfa_at` fresco do JWT) + chave PIX
  do mesmo CPF.
- **Extrato + saldo**; múltiplas wallets por usuário (a decidir no spec — provável: uma real + uma sandbox).
- **Sandbox wallet** (dinheiro virtual pros jogos): decidir se vive no wallet service ou por serviço — inclinação: no
  wallet service, flag `sandbox`, nunca conversível.

## 3. Assinaturas/billing

- Recorrência debitando saldo da wallet (sem gateway de cartão).
- Bloqueio de acesso por serviço: claim/scope de assinatura ativa vs verificação runtime — decidir no spec.
- Modelos: crédito (consumo, ex. dfe por documento) vs preço cheio (mensalidade, ex. poker).
- Renovação: job que tenta débito; saldo insuficiente → grace period → suspensão.

## 4. Integração poker/dominó

- Consomem wallet via API interna (client_credentials, scopes `internal:wallet:*`).
- Apostas: débito/crédito idempotente por rodada/mesa; nunca saldo negativo.
- Sandbox wallet pra jogo grátis/treino.
- 18+ e KYC já garantidos pelo `kyc_level` (regra centralizada no account).

## Pendências operacionais (independente do roadmap)

Rollout de produção do hardening (antes de qualquer deploy do backend novo):

1. `go run ./cmd/rotatekeys -env prod -init` — **antes** do deploy (boot sem `RSA_PRIVATE_KEY` fatal-erra se
   `jwk/active` não existir no SSM).
2. Deploy CDK (tabela `{env}_account_audit`, IAM `ssm:PutParameter` em `jwk/*`, user-data sem RSA key) + backend.
3. Verificar `/.well-known/jwks.json` servindo o mesmo KID (ctech-dfe intocado).
4. Opcional: teste de rotação forçada.

Quando KYC for implantado: re-rodar `cmd/seedscopes` por ambiente; seed do client M2M da wallet (confidential +
first_party + `allowed_scopes: ["internal:kyc"]`) quando a wallet nascer.
