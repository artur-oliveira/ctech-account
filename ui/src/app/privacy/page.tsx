import type {Metadata} from 'next'
import {LegalPageLayout, LegalSection} from '@/components/legal-page-layout'

export const metadata: Metadata = {
  title: 'Política de Privacidade',
}

const PRIVACY_VERSION = '3.0'
const UPDATED_AT = '15 de julho de 2026'

export default function PrivacyPage() {
  return (
    <LegalPageLayout
      title="Política de Privacidade"
      updatedAt={UPDATED_AT}
    >
      <p className="text-xs text-muted-foreground">
        Versão {PRIVACY_VERSION}
      </p>

      <LegalSection heading="1. Controlador dos dados">
        <p>
          Esta Política de Privacidade descreve como a{' '}
          <strong>A O CARVALHO TECH</strong>, inscrita no CNPJ sob o nº
          62.787.449/0001-07, com sede na Rua Atleta Daniel Aragão Matos,
          nº 6201, Vale Quem Tem, Teresina/PI, Brasil, realiza o tratamento
          de dados pessoais no âmbito da plataforma CTech.
        </p>

        <p>
          Para fins da Lei nº 13.709/2018 (Lei Geral de Proteção de Dados
          Pessoais – LGPD), a CTech atua como Controladora dos dados
          tratados pelo serviço CTech Account.
        </p>

        <p>
          Encarregado pelo tratamento de dados (DPO):
          <strong> Artur Oliveira Carvalho</strong>.
        </p>

        <p>
          Contatos:
        </p>

        <ul className="list-disc pl-5 space-y-2">
          <li>dpo@aoctech.app</li>
          <li>legal@aoctech.app</li>
          <li>(86) 9 8803-3430</li>
        </ul>
      </LegalSection>

      <LegalSection heading="2. Escopo">
        <p>
          Esta Política aplica-se ao CTech Account e aos tratamentos de dados
          necessários para autenticação, gerenciamento de contas, segurança e
          utilização dos serviços integrados da plataforma CTech.
        </p>

        <p>
          Alguns produtos poderão possuir políticas ou aditivos específicos,
          prevalecendo as disposições mais específicas para o respectivo
          serviço.
        </p>
      </LegalSection>

      <LegalSection heading="3. Dados pessoais coletados">
        <ul className="list-disc pl-5 space-y-2">
          <li>
            <strong>Dados de cadastro:</strong> nome, sobrenome, endereço de
            e-mail, senha criptografada e preferências de conta.
          </li>

          <li>
            <strong>Dados de autenticação:</strong> informações relacionadas
            a MFA, TOTP, passkeys (WebAuthn) e sessões autenticadas.
          </li>

          <li>
            <strong>Dados de login social:</strong> nome, endereço de e-mail
            e foto de perfil fornecidos pelo Google, quando o usuário optar
            pelo login social.
          </li>

          <li>
            <strong>Dados de KYC:</strong> CPF, nome completo, data de
            nascimento, endereço, documentos comprobatórios, documentos de
            identificação e demais informações necessárias para verificação
            de identidade.
          </li>

          <li>
            <strong>Dados biométricos (KYC):</strong> quatro vídeos curtos de
            selfie (rosto virado para cima, para baixo, para a esquerda e
            para a direita), capturados pela câmera do dispositivo durante a
            verificação de identidade. Usados exclusivamente para que um
            analista humano confirme a presença e a vivacidade da pessoa no
            momento do envio, sem reconhecimento facial automatizado nem
            decisão automatizada.
          </li>

          <li>
            <strong>Dados técnicos:</strong> endereço IP, user-agent,
            navegador, sistema operacional, idioma, identificadores de
            dispositivo, localização aproximada derivada do IP e informações
            de sessão.
          </li>

          <li>
            <strong>Dados de auditoria:</strong> registros de login,
            operações realizadas, alterações de segurança, utilização de
            APIs e demais eventos relevantes para fins de rastreabilidade.
          </li>
        </ul>
      </LegalSection>

      <LegalSection heading="4. Finalidades do tratamento">
        <p>
          Os dados pessoais poderão ser tratados para:
        </p>

        <ul className="list-disc pl-5 space-y-2">
          <li>criação e gerenciamento de contas;</li>
          <li>autenticação e autorização de usuários;</li>
          <li>prevenção de fraudes e abusos;</li>
          <li>cumprimento de obrigações legais e regulatórias;</li>
          <li>investigações de incidentes de segurança;</li>
          <li>prestação dos serviços contratados;</li>
          <li>proteção dos direitos da CTech e de terceiros;</li>
          <li>suporte técnico e atendimento;</li>
          <li>desenvolvimento de novas funcionalidades;</li>
          <li>integrações via OAuth 2.0 e OpenID Connect.</li>
        </ul>
      </LegalSection>

      <LegalSection heading="5. Bases legais">
        <p>
          O tratamento poderá ocorrer com fundamento nas seguintes bases
          legais previstas na LGPD:
        </p>

        <ul className="list-disc pl-5 space-y-2">
          <li>execução de contrato;</li>
          <li>cumprimento de obrigação legal ou regulatória;</li>
          <li>exercício regular de direitos;</li>
          <li>proteção do crédito, quando aplicável;</li>
          <li>legítimo interesse;</li>
          <li>consentimento, quando necessário.</li>
        </ul>

        <p>
          Por se tratar de dado pessoal sensível nos termos do artigo 5º, II,
          da LGPD, o tratamento dos vídeos de selfie (dados biométricos) tem
          como base legal o <strong>consentimento específico e destacado</strong>
          do titular, obtido no momento do envio dos vídeos, nos termos do
          artigo 11, I, da LGPD. O titular pode revogar esse consentimento a
          qualquer momento, observado que a revogação poderá impedir o acesso
          a funcionalidades que exigem identidade verificada.
        </p>
      </LegalSection>

      <LegalSection heading="6. Compartilhamento de dados">
        <p>
          Os dados poderão ser compartilhados com:
        </p>

        <ul className="list-disc pl-5 space-y-2">
          <li>serviços integrados da plataforma CTech;</li>
          <li>prestadores de serviços de infraestrutura;</li>
          <li>provedores de autenticação;</li>
          <li>instituições financeiras parceiras;</li>
          <li>autoridades públicas e órgãos reguladores;</li>
          <li>terceiros quando exigido por lei ou ordem judicial.</li>
        </ul>

        <p>
          Atualmente a infraestrutura da plataforma utiliza serviços da
          Amazon Web Services (AWS).
        </p>

        <p>
          A CTech não comercializa dados pessoais de seus usuários.
        </p>
      </LegalSection>

      <LegalSection heading="7. Transferência internacional">
        <p>
          Em razão da utilização de provedores globais de infraestrutura,
          determinados dados poderão ser armazenados ou processados fora do
          território brasileiro.
        </p>

        <p>
          Nessas hipóteses, a CTech adotará medidas razoáveis para garantir
          nível adequado de proteção aos dados pessoais, observando o disposto
          nos artigos 33 e seguintes da LGPD.
        </p>
      </LegalSection>

      <LegalSection heading="8. Retenção dos dados">
        <p>
          Os dados pessoais serão mantidos apenas pelo tempo necessário para
          o cumprimento das finalidades para as quais foram coletados,
          observadas as obrigações legais e regulatórias aplicáveis.
        </p>

        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-sm">
            <thead>
            <tr>
              <th className="border p-2 text-left">
                Categoria
              </th>
              <th className="border p-2 text-left">
                Prazo de retenção
              </th>
            </tr>
            </thead>

            <tbody>
            <tr>
              <td className="border p-2">
                Dados cadastrais
              </td>
              <td className="border p-2">
                Enquanto a conta permanecer ativa e pelo prazo necessário
                ao exercício regular de direitos.
              </td>
            </tr>

            <tr>
              <td className="border p-2">
                Logs e auditoria
              </td>
              <td className="border p-2">
                Prazo indeterminado, para prevenção a fraudes,
                investigações e auditorias.
              </td>
            </tr>

            <tr>
              <td className="border p-2">
                Dados de KYC
              </td>
              <td className="border p-2">
                Até a conclusão da verificação, ressalvadas obrigações
                legais de retenção.
              </td>
            </tr>

            <tr>
              <td className="border p-2">
                Dados biométricos (vídeos de selfie)
              </td>
              <td className="border p-2">
                Até a decisão do analista humano sobre a verificação;
                excluídos após a aprovação ou em caso de rejeição que exija
                novo envio, ressalvadas obrigações legais de retenção.
              </td>
            </tr>

            <tr>
              <td className="border p-2">
                Dados necessários ao cumprimento de obrigações legais
              </td>
              <td className="border p-2">
                Pelo prazo exigido pela legislação aplicável.
              </td>
            </tr>
            </tbody>
          </table>
        </div>
      </LegalSection>

      <LegalSection heading="9. Segurança da informação">
        <p>
          A CTech adota medidas técnicas e administrativas adequadas para
          proteção dos dados pessoais, incluindo:
        </p>

        <ul className="list-disc pl-5 space-y-2">
          <li>criptografia em trânsito mediante TLS;</li>
          <li>armazenamento seguro de credenciais;</li>
          <li>hash de senhas utilizando Argon2id;</li>
          <li>controle de acesso baseado em privilégios mínimos;</li>
          <li>autenticação multifator;</li>
          <li>registro de eventos de segurança;</li>
          <li>monitoramento de atividades suspeitas.</li>
        </ul>
      </LegalSection>

      <LegalSection heading="10. Decisões automatizadas e prevenção à fraude">
        <p>
          A CTech poderá utilizar mecanismos automatizados para análise de
          risco, prevenção à fraude, detecção de comportamentos abusivos e
          proteção dos usuários e da plataforma.
        </p>

        <p>
          Tais mecanismos poderão resultar em verificações adicionais,
          limitações temporárias ou bloqueios preventivos de determinadas
          funcionalidades.
        </p>
      </LegalSection>

      <LegalSection heading="11. Incidentes de segurança">
        <p>
          Em caso de incidente de segurança que possa acarretar risco ou dano
          relevante aos titulares de dados, a CTech adotará as medidas
          previstas pela legislação aplicável, incluindo eventual comunicação
          à Autoridade Nacional de Proteção de Dados (ANPD) e aos titulares
          afetados, quando necessário.
        </p>
      </LegalSection>

      <LegalSection heading="12. Direitos dos titulares">
        <p>
          Nos termos do artigo 18 da LGPD, o titular poderá solicitar:
        </p>

        <ul className="list-disc pl-5 space-y-2">
          <li>confirmação da existência de tratamento;</li>
          <li>acesso aos dados;</li>
          <li>correção de dados incompletos ou desatualizados;</li>
          <li>anonimização, bloqueio ou eliminação;</li>
          <li>portabilidade dos dados;</li>
          <li>informações sobre compartilhamentos;</li>
          <li>revogação do consentimento;</li>
          <li>oposição ao tratamento, quando cabível;</li>
          <li>peticionamento perante a ANPD.</li>
        </ul>

        <p>
          Solicitações poderão ser encaminhadas ao Encarregado por meio dos
          canais de contato disponibilizados nesta Política.
        </p>
      </LegalSection>

      <LegalSection heading="13. Menores de idade">
        <p>
          Os serviços da CTech não são destinados a menores de 18 (dezoito)
          anos.
        </p>

        <p>
          Caso seja identificado tratamento indevido de dados de menores, a
          CTech poderá adotar medidas para exclusão ou bloqueio das
          informações, observadas as obrigações legais aplicáveis.
        </p>
      </LegalSection>

      <LegalSection heading="14. Cookies">
        <p>
          Atualmente a plataforma utiliza exclusivamente cookies estritamente
          necessários ao funcionamento dos serviços, autenticação e segurança
          das sessões.
        </p>

        <p>
          A utilização desses cookies independe de consentimento, nos termos
          da legislação aplicável.
        </p>
      </LegalSection>

      <LegalSection heading="15. Alterações desta Política">
        <p>
          Esta Política poderá ser alterada periodicamente.
        </p>

        <p>
          Alterações relevantes serão comunicadas por meio da plataforma,
          correio eletrônico ou outros meios adequados.
        </p>

        <p>
          A versão vigente será sempre a disponibilizada nesta página.
        </p>
      </LegalSection>

      <LegalSection heading="16. Contato">
        <p>
          Em caso de dúvidas, solicitações ou exercício de direitos previstos
          na LGPD, entre em contato:
        </p>

        <ul className="list-disc pl-5 space-y-2">
          <li>Encarregado: Artur Oliveira Carvalho</li>
          <li>dpo@aoctech.app</li>
          <li>legal@aoctech.app</li>
          <li>(86) 9 8803-3430</li>
        </ul>
      </LegalSection>
    </LegalPageLayout>
  )
}