import type { Metadata } from 'next'
import { LegalPageLayout, LegalSection } from '@/components/legal-page-layout'

export const metadata: Metadata = {
  title: 'Política de Privacidade',
}

const PRIVACY_VERSION = '1.0'
const UPDATED_AT = '10 de julho de 2026'

export default function PrivacyPage() {
  return (
    <LegalPageLayout title="Política de Privacidade" updatedAt={UPDATED_AT}>
      <p className="text-xs text-muted-foreground">Versão {PRIVACY_VERSION}</p>

      <LegalSection heading="1. Controlador dos dados">
        <p>
          O controlador dos dados pessoais tratados nesta Política, nos termos da Lei nº 13.709/2018 (Lei Geral de
          Proteção de Dados — LGPD), é <strong>A O CARVALHO TECH</strong>, CNPJ 62.787.449/0001-07, com sede na Rua
          Atleta Daniel Aragão Matos, 6201, Vale Quem Tem, Teresina/PI. Contato do Encarregado (DPO):{' '}
          <a href="mailto:dpo@aoctech.app" className="underline underline-offset-4">
            dpo@aoctech.app
          </a>
          .
        </p>
      </LegalSection>

      <LegalSection heading="2. Escopo">
        <p>
          Esta Política descreve o tratamento de dados pessoais realizado pelo CTech Account, o serviço de
          identidade único da plataforma CTech. Produtos que utilizam o CTech Account para login (ex.: CTech DFe,
          CTech Wallet) podem tratar dados adicionais, específicos de sua finalidade — nesses casos, um aditivo de
          privacidade próprio do produto complementa esta Política.
        </p>
      </LegalSection>

      <LegalSection heading="3. Dados que coletamos">
        <ul className="list-disc space-y-2 pl-5">
          <li>
            <strong>Dados de cadastro:</strong> nome, sobrenome, e-mail e senha (armazenada apenas como hash
            criptográfico, nunca em texto plano).
          </li>
          <li>
            <strong>Dados de verificação de identidade (KYC):</strong> CPF, nome completo conforme Receita Federal e
            data de nascimento — coletados apenas quando você opta por verificar sua identidade para acessar
            recursos que exigem essa verificação.
          </li>
          <li>
            <strong>Dados de autenticação multifator:</strong> segredos de TOTP e chaves públicas de passkeys
            (WebAuthn) — as chaves privadas nunca saem do seu dispositivo.
          </li>
          <li>
            <strong>Dados de uso e sessão:</strong> endereço IP, tipo/versão de navegador, sistema operacional,
            identificador de dispositivo, localização aproximada (cidade/região, derivada do IP) e registros de
            login, para segurança da conta e detecção de fraude.
          </li>
          <li>
            <strong>Dados de terceiros (login social):</strong> quando você usa &ldquo;Entrar com Google&rdquo;,
            recebemos seu nome, e-mail e foto de perfil conforme autorizado por você naquele provedor.
          </li>
          <li>
            <strong>Cookies:</strong> cookies estritamente necessários para manter sua sessão autenticada. Não
            utilizamos cookies de rastreamento publicitário.
          </li>
        </ul>
      </LegalSection>

      <LegalSection heading="4. Finalidade e base legal">
        <p>Tratamos seus dados pessoais com as seguintes finalidades e bases legais (art. 7º da LGPD):</p>
        <ul className="list-disc space-y-2 pl-5">
          <li>
            <strong>Execução de contrato:</strong> criar e gerenciar sua conta, autenticar seu acesso e prestar os
            serviços da Plataforma.
          </li>
          <li>
            <strong>Cumprimento de obrigação legal ou regulatória:</strong> verificação de identidade e idade mínima
            para recursos financeiros, prevenção à fraude e ao uso indevido.
          </li>
          <li>
            <strong>Legítimo interesse:</strong> segurança da conta (detecção de login suspeito, reuso de token),
            registros de auditoria e melhoria da Plataforma — sempre de forma proporcional e sem prejudicar seus
            direitos fundamentais.
          </li>
          <li>
            <strong>Consentimento:</strong> login social (Google) e recursos que você opta ativamente por habilitar.
          </li>
        </ul>
      </LegalSection>

      <LegalSection heading="5. Com quem compartilhamos">
        <ul className="list-disc space-y-2 pl-5">
          <li>
            <strong>Produtos da plataforma CTech</strong> que você escolhe usar (ex.: CTech DFe, CTech Wallet) —
            recebem apenas as informações mínimas de identidade necessárias (nome, e-mail, nível de verificação),
            nunca sua senha.
          </li>
          <li>
            <strong>Provedores de infraestrutura</strong> (hospedagem em nuvem, envio de e-mail transacional),
            atuando como operadores, sob instruções contratuais e obrigações de confidencialidade.
          </li>
          <li>
            <strong>Google</strong>, exclusivamente quando você opta por &ldquo;Entrar com Google&rdquo;.
          </li>
          <li>Autoridades públicas, quando exigido por lei, ordem judicial ou para proteção de direitos.</li>
        </ul>
        <p>Não vendemos seus dados pessoais a terceiros.</p>
      </LegalSection>

      <LegalSection heading="6. Transferência internacional">
        <p>
          Nossa infraestrutura de nuvem pode armazenar e processar dados em servidores localizados fora do Brasil.
          Nesses casos, adotamos as salvaguardas exigidas pela LGPD (art. 33) para assegurar nível de proteção
          adequado aos dados transferidos.
        </p>
      </LegalSection>

      <LegalSection heading="7. Retenção de dados">
        <p>
          Mantemos seus dados pessoais enquanto sua conta estiver ativa e pelo prazo adicional necessário para
          cumprir obrigações legais, regulatórias, resolver disputas e fazer cumprir nossos acordos. Registros de
          auditoria e segurança são mantidos por período compatível com sua finalidade de prevenção à fraude. Ao
          solicitar a exclusão da conta, os dados são removidos ou anonimizados, ressalvado o que a lei exigir que
          seja retido.
        </p>
      </LegalSection>

      <LegalSection heading="8. Segurança da informação">
        <p>
          Adotamos medidas técnicas e organizacionais para proteger seus dados, incluindo: senhas armazenadas com
          algoritmo de hash com custo computacional (Argon2id), comunicação criptografada (TLS), autenticação
          multifator opcional (TOTP, passkeys/WebAuthn), detecção de reuso de tokens de sessão e registro de
          auditoria de eventos de segurança da conta.
        </p>
      </LegalSection>

      <LegalSection heading="9. Seus direitos">
        <p>Nos termos do art. 18 da LGPD, você pode, mediante solicitação ao nosso Encarregado (DPO):</p>
        <ul className="list-disc space-y-2 pl-5">
          <li>Confirmar a existência de tratamento e acessar seus dados;</li>
          <li>Corrigir dados incompletos, inexatos ou desatualizados;</li>
          <li>Solicitar anonimização, bloqueio ou eliminação de dados desnecessários ou tratados em desconformidade com a lei;</li>
          <li>Solicitar a portabilidade dos seus dados a outro fornecedor;</li>
          <li>Revogar o consentimento e se opor a tratamentos baseados em legítimo interesse, quando aplicável;</li>
          <li>Solicitar informações sobre com quem compartilhamos seus dados;</li>
          <li>Peticionar contra nós perante a Autoridade Nacional de Proteção de Dados (ANPD).</li>
        </ul>
        <p>
          Para exercer esses direitos, entre em contato com{' '}
          <a href="mailto:dpo@aoctech.app" className="underline underline-offset-4">
            dpo@aoctech.app
          </a>
          . Responderemos dentro do prazo legal aplicável.
        </p>
      </LegalSection>

      <LegalSection heading="10. Menores de idade">
        <p>
          A Plataforma não é destinada a menores de 18 anos. Não coletamos intencionalmente dados de menores de
          idade. Se tomarmos conhecimento de que uma conta pertence a um menor, ela será encerrada.
        </p>
      </LegalSection>

      <LegalSection heading="11. Alterações desta Política">
        <p>
          Podemos atualizar esta Política periodicamente. Alterações materiais serão comunicadas por e-mail ou aviso
          na Plataforma antes de entrarem em vigor. A versão vigente é sempre a publicada nesta página, identificada
          pelo número de versão no topo do documento.
        </p>
      </LegalSection>

      <LegalSection heading="12. Contato">
        <p>
          Dúvidas sobre esta Política ou sobre o tratamento dos seus dados podem ser enviadas ao nosso Encarregado
          (DPO) em{' '}
          <a href="mailto:dpo@aoctech.app" className="underline underline-offset-4">
            dpo@aoctech.app
          </a>
          .
        </p>
      </LegalSection>
    </LegalPageLayout>
  )
}
