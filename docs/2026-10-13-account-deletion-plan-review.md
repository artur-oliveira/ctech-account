# Revisão do plano de exclusão de conta e da coordenação entre serviços

- **Data:** 2026-10-13
- **Escopo:** specs `2026-10-06-account-deletion-*`, planos das fases 1 a 4 do ctech-account e os planos de participante de
  wallet, dfe, poker e billing (branches `docs/account-deletion-plan` de cada repositório, **ainda não mescladas em `main`**).
- **Método:** cada plano foi comparado com o código e os docs de `main` de cada repo em 2026-10-13.
- **Conclusão:** o desenho do saga (orquestração, ack, tombstone, revogação de JWT) está sólido. O problema é **deriva**: os
  planos foram escritos em 07 e 08/10 e todos os serviços receberam features novas depois. Poker é o único plano
  praticamente atual. Billing é o mais defasado; wallet tem uma premissa central invalidada; dfe tem dois pontos de deriva.

## 1. Estado de implementação

| Peça | Estado em `main` |
|---|---|
| ctech-go-common `erasure` + `jwtverify` revogação | Implementado (v1.13.0 em diante) |
| ctech-account fase 1 (ciclo de vida, lock) | Implementado, atrás de `ACCOUNT_DELETION_ENABLED` |
| ctech-account fases 2a, 2b, 3, 4 | Apenas planos |
| Participantes wallet, dfe, poker, billing | Apenas planos, em branches não mescladas |
| Decisões de 2026-10-08 (cliente dedicado de ack, `cpf_hmac`, issuer, blockers do poker) | Só na branch `docs/account-deletion-plan-decisions` do account; a fase 3 em `main` não as tem |

Defeito imediato: `docs/README.md` tem marcadores de conflito de merge commitados (`<<<<<<<`, `=======`, `>>>>>>>`). Corrigido junto com este documento.

## 2. Problemas transversais (todos os planos)

1. **Versão do api-commons.** Os planos exigem `v1.13.1` exato (poker `v1.14.0`). Hoje: account, wallet, dfe, poker em `v1.16.0`, billing em
   `v1.18.0`. Seguir o plano literalmente (`go get ...@v1.13.1`, "do not bump") **rebaixa** os módulos. Trocar por "versão mínima com o
   pacote `erasure` (v1.13.0)" e deixar o `go.mod` atual.
2. **Nomes de escopo no go-common.** O README do go-common ainda diz `account:erasure:ack` e filtro `MessageBody`; o contrato vigente é
   `internal:account:erasure-ack` e filtro por atributo. A fase 3 prevê corrigir (Task 8); não aconteceu.
3. **Inventário de dados defasado.** O inventário (§4) ainda descreve Asaas, `wallet_baas_accounts`, MED e transfer intents (removidos em
   07/10) e não lista a tabela `byok` do billing nem o rail Inter restaurado.
4. **A mensagem `user.erase` não carrega o tipo do workspace.** `organizations[]` traz só ids. O billing precisa distinguir workspace
   `personal` de `organization` (ver 3.1) e hoje não consegue.
5. **Nada obriga features novas a nascerem com fluxo de exclusão.** Foi exatamente isso que causou a deriva. Regra adicionada aos READMEs
   (seção 8).

## 3. ctech-billing (o mais defasado)

O plano foi escrito sobre o billing de `3412381` (#43). Desde então entraram 62 commits. Itens, por gravidade:

### 3.1 Bloqueantes (o plano falha ou apaga/guarda errado)

- **Task 1 rebaixa o módulo.** Billing está em api-commons `v1.18.0`; a task faz `go get @v1.13.1`.
- **`OrganizationRepository.GetByOwner` não existe mais** (removido no #99, ADR 0028). O blocker `billing.tenant_owner` e a Task 6 dependem dele.
  Além do método, a premissa mudou: o tenant do merchant é achado pelo workspace da ctech-account
  (`GetByAccountOrganization`), e `owner_user_id` não concede nada.
- **Tabelas `cards`, `imports` e `byok` fora do purge.** O plano tem `financeTables` com 4 tabelas (ledger_accounts, ledger_transactions,
  bills, recurrences). O próprio inventário (§6) lista `cards` e `imports`. O teste `TestEveryTableIsClassifiedForThePurge` falharia na
  primeira execução, o que é bom, mas o trabalho não está planejado:
  - `cards`: partições `S#CARD#{id}` (cartão, compras, faturas, itens), além de `S`.
  - `imports`: `S#IMPORT#{id}` (batches e linhas, 90 dias), travas `FITID#` (sem TTL), `CSVMAP#`.
  - `byok`: partição da conexão (= chave do space da organização), `SEALED#` (credenciais e certificado mTLS do banco),
    partições `HOOK#{id}` e `CHARGEREF#...` que **não** estão sob `S` e não são achadas pelo purge por prefixo.
- **Clientes de organização (`CUSTOMER_ORG#`, `ORG_{id}`) não são tratados.** O plano os declara "fora de escopo, não construído". Foram
  construídos (#75) e o dfe agora assina o plano via cliente `ORG_{id}` (dfe #130). Quando a organização some junto com o único membro:
  nada cancela a assinatura da organização (continua sendo faturada), o cliente com nome, e-mail e CNPJ (D12: MEI é dado pessoal) não é
  anonimizado, e faturas abertas da organização não bloqueiam. `Eraser.Purge` só olha `GetByUser`.
- **Tenant merchant vinculado ao workspace apagado: sem regra.** Um workspace de membro único pode ter tenant no billing (organização,
  credenciais, webhooks, clientes finais, conexão BYOK com credenciais seladas). O plano apaga apenas os spaces financeiros.
  **Decisão necessária** (ver seção 7).
- **`Personal()` não significa o que o plano assume.** `ResolvedSpace.Personal()` é só o space padrão `USER#{sub}`. `space.ForJob` devolve
  sempre `KindOrganization`. Logo, a regra R12 ("auditoria é apagada só em space pessoal") **mantém por 5 anos** as linhas de auditoria de
  um workspace `personal` adicional apagado, contrariando o ADR 0026.

### 3.2 Importantes

- **`AllByCustomer` lê a partição inteira do tenant zero** (todas as faturas e assinaturas de todos os clientes) em cada eligibility, cada
  purge e a cada reentrega. Desde #85, #88, #91 e #92 existem `lookup-index` e `customer-index` por cliente. Usar `ListByCustomer`
  paginado, **com a condição** de que os backfills (`cmd/backfill-*-lookup`) tenham rodado em prod: índice esparso que falha por não ter
  sido preenchido deixa passar fatura aberta e a exclusão segue (erro silencioso em dinheiro). Adicionar verificação de backfill como gate.
- **`applications` (candidaturas de merchant) na partição do tenant zero:** `company_tax_id` (selado), `contact_email`, `applied_by` (sub),
  `description`, `website`. Sem tratamento. Anonimizar/apagar quando o candidato é apagado.
- **`usage`: classificação errada.** O plano diz "24 meses de TTL, sem dado pessoal". As linhas `LATEST` de nível (`levels.go`) são
  `RetentionPermanent`, por `customer_ref` (tenant zero usa `USER_{sub}` / `ORG_{id}`). A ctech-account reporta níveis de espaços e pessoas
  por `USER_{sub}` (plan limits). Decidir: apagar as linhas `LATEST` do sub ou documentar como identificador opaco retido.
- **Credenciais e webhooks do console** (`connected_by`, `authorized_by` no BYOK, URLs e segredos de webhook): sem linha no
  `notSpaceScoped` com justificativa real. Credenciais de um tenant apagado precisam ser revogadas.
- **Ator na auditoria e no ledger de space compartilhado que sobrevive:** `Actor` guarda o sub da pessoa apagada. O inventário do dfe
  anonimiza o ator; o do billing é omisso. Decidir e escrever.
- **BYOK em terceiros (art. 18 §6):** apagar o webhook registrado no Inter e revogar o acesso ao desconectar; o consumidor
  (`byok-webhook`, `byok-outbound`, sweeps, `finance`) precisa checar `OrgErased` (a Task 8 cobre só o job financeiro).
- **Fila de replay financeiro** (`schedule-index` de `invoices`): paga fatura pendente de postagem num space apagado deve sair da fila ou
  ser ignorada pelo tombstone; hoje não está no plano.
- **Cache de membership (60 s) e `Verifier.Middleware`:** o choke point do lock (R6) continua único (`router.go:162`), mas o plano deve
  reconfirmar que as rotas de Finanças e do console novas passam por ele.

### 3.3 Correções de texto

- Remover "Organization customers: not built yet" de Out of scope.
- R4 (só tenant zero é apagado) continua correta para clientes de merchants terceiros, mas precisa de exceção explícita para o tenant
  vinculado ao workspace apagado.
- Blocker `billing.tenant_owner` vira "workspace apagado possui tenant ativo" (ou some, conforme a decisão 7.1).

## 4. ctech-wallet

A premissa central do plano caiu. O plano diz "não existe rail de depósito nem de saque" (R6, R7, R8). Em 08/10 o rail Inter foi
restaurado (spec `2026-10-08-wallet-restoration-design.md`): depósito com match de CPF e estorno, saque com `RequireRecentMFA`, limites
diários por carteira, quarentena de depósito sem CPF do pagador, `real_daily_counters`.

- **`wallet.balance_nonzero` precisa cobrir `real` e `game`** (inventário §4, invariante 9: `real + game` é dinheiro real). R8 trata `real`
  como inalcançável; agora é o caso principal.
- **Saque de encerramento esbarra nos limites diários** (`daily_withdraw_cap` R$ 1.000, `daily_withdraw_count` 1). Quem tem mais que o teto
  precisa de vários dias, e a conta fica bloqueada desde a confirmação. O fluxo de UI da conta deve avisar isso antes do pedido; D7 (sem
  mínimo) continua válido, mas o teto diário não estava no cálculo.
- **Depósito pago após o lock ou após o purge.** `ConfirmDeposit` fica aberto (correto), mas o match de CPF usa o KYC da conta e a conta
  pode já estar purgada: o caminho tem que ser **estorno automático ao pagador**, não "support refunda" (R14).
- **Blockers adicionais:** estorno de depósito em andamento (`refund_pending`), depósito em quarentena, saque `processing`.
- **R3** deve incluir `real_daily_counters` na limpeza do `wallet_users`.
- Versão do api-commons: ver seção 2, item 1. Tabelas aposentadas continuam corretamente fora do purge.

## 5. ctech-dfe, ctech-poker, ctech-go-common

**ctech-dfe**
- A assinatura passou a ser da **organização** (#130, #132): `account_billing` agora é chaveada por organização da ctech-account
  (`ORG_...`, `LEVEL_DIRTY_{org}#meter`, contadores, snapshot). O catálogo do plano marca `account_billing` como `ScopePerson`
  (`USER_{sub}`); precisa de escopo de organização para os ids de `organizations[]` (linhas legadas `USER_` seguem para quem não migrou).
- R4 propõe criar o GSI `organization-id-index`; já existe `organization-index` (`organization_id` + `created_at`, KEYS_ONLY, esparso)
  desde #130. Reusar.
- Coordenação com o billing: a assinatura do dfe vive como cliente `ORG_{id}` no billing. O apagamento de uma organização no dfe só
  fecha o ciclo se o billing cancelar essa assinatura (ver 3.1).

**ctech-poker**: as 37 tabelas do CDK estão no plano e há classificação por tabela. Só dois commits posteriores (#422 paginação de
arestas sociais, #424 UI); a paginação reforça que o purge de arestas precisa paginar até o fim. Único ajuste: versão do api-commons.

**ctech-go-common**: corrigir o README do `erasure` (escopo e filtro), e a seção "consumidores" do CLAUDE.md está defasada
(`v1.9.1`).

## 6. ctech-account (orquestrador)

- Mesclar as decisões de 2026-10-08 (branch `docs/account-deletion-plan-decisions`) na fase 3 e na fase 4.
- **Features novas do account que a fase 2a/2b não purga** (plan limits de 10/10, `docs/specs/2026-10-10-space-plan-limits.md`):
  `OWNER#{sub}` / `PERSONAL_SPACES` (contadores) e as linhas `LEVEL_DIRTY` (`owner_user_id`) em `account_organizations`. O worker de
  níveis não pode reportar ao billing níveis de um sub apagado.
- Workspaces `personal` compartilhados já têm regra (bloqueia até transferir); falta definir como `kind` chega aos participantes (2.4).
- Regra de manutenção (seção 8).

## 7. Decisões (tomadas em 2026-10-13)

1. **Tenant merchant de um workspace apagado: bloquear.** Novo blocker `billing.merchant_tenant` (fale com o suporte). Exclusão de
   tenant fica como feature própria, com seu próprio fluxo de exclusão.
2. **Auditoria de workspace apagado: apagar sempre**, de qualquer tipo, quando o workspace está na lista da mensagem.
3. **Ator (sub) em auditoria e ledger de space compartilhado que sobrevive: reter como identificador opaco.** O sub já é a chave que
   o tombstone mantém, não carrega dado pessoal e o ledger é imutável; anonimizar exigiria reescrever histórico.
4. **Linhas `LATEST` de nível por `USER_{sub}` / `ORG_{id}`: apagar.**
5. **`user.erase` ganha `workspaces[]` com `{id, kind}`** (campo aditivo, versão 1; `organizations[]` continua como lista de ids).

## 8. Regra de manutenção (adicionada aos READMEs)

Toda feature nova que guarde dado ligado a uma pessoa ou organização, depois do plano de exclusão, só está pronta quando o fluxo de
exclusão dela existe e foi testado. Está escrita na seção "Account deletion" do README de cada repositório. Mecanismo que a torna
verificável: teste que falha quando uma tabela nova não está classificada no purge (billing e dfe já planejam; wallet e poker devem
ter o equivalente).

## 9. Ordem sugerida

1. Corrigir `docs/README.md` (feito) e mesclar as branches de plano em `main`.
2. Resolver as decisões da seção 7.
3. Reescrever o plano do billing (rebaseline) e o do wallet; ajustes pontuais no dfe e no go-common.
4. Só então executar as fases do account (2a, 2b, 3, 4) e os participantes na ordem poker, billing, dfe, wallet.
