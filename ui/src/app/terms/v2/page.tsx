import type {Metadata} from 'next'
import {LegalPageLayout, LegalSection, TERMS_VERSION_HISTORY} from '@/components/legal-page-layout'

export const metadata: Metadata = {
  title: 'Termos de Uso',
}

const TOS_VERSION = '2.0'
const UPDATED_AT = '12 de julho de 2026'

export default function TermsPage() {
  return (
    <LegalPageLayout title="Termos de Uso" version={TOS_VERSION} updatedAt={UPDATED_AT} versionHistory={TERMS_VERSION_HISTORY}>

      <LegalSection heading="1. Quem somos">
        <p>
          Estes Termos de Uso regulam o acesso e utilização da plataforma
          CTech, operada por <strong>A O CARVALHO TECH</strong>, inscrita no
          CNPJ nº 62.787.449/0001-07, com sede na Rua Atleta Daniel Aragão
          Matos, nº 6201, Vale Quem Tem, Teresina/PI, Brasil.
        </p>

        <p>
          A plataforma compreende o serviço de identidade digital
          <strong> CTech Account</strong> e os demais produtos e serviços
          integrados, incluindo, entre outros, CTech DF-e, CTech Wallet,
          CTech Billing e futuros serviços disponibilizados pela CTech.
        </p>
      </LegalSection>

      <LegalSection heading="2. Aceite">
        <p>
          Ao criar uma conta, acessar ou utilizar qualquer funcionalidade da
          plataforma, você declara ter lido, compreendido e aceitado estes
          Termos de Uso e a Política de Privacidade aplicável.
        </p>

        <p>
          Caso não concorde com qualquer disposição destes documentos, você
          não deverá utilizar os serviços.
        </p>
      </LegalSection>

      <LegalSection heading="3. Elegibilidade">
        <ul className="list-disc pl-5 space-y-2">
          <li>
            A utilização da plataforma é destinada exclusivamente a pessoas
            maiores de 18 (dezoito) anos.
          </li>

          <li>
            Você declara possuir capacidade civil plena para celebrar este
            contrato.
          </li>

          <li>
            Informações falsas, incompletas ou enganosas poderão resultar em
            suspensão ou encerramento da conta.
          </li>

          <li>
            Cada pessoa poderá possuir apenas uma conta principal, salvo
            autorização expressa da CTech.
          </li>
        </ul>
      </LegalSection>

      <LegalSection heading="4. Cadastro e segurança da conta">
        <p>
          Você é responsável pela confidencialidade de suas credenciais,
          incluindo senha, códigos MFA, passkeys, dispositivos autenticadores
          e quaisquer outros fatores de autenticação utilizados.
        </p>

        <p>
          O usuário deverá comunicar imediatamente a CTech caso suspeite de:
        </p>

        <ul className="list-disc pl-5 space-y-2">
          <li>acesso não autorizado;</li>
          <li>comprometimento de credenciais;</li>
          <li>perda de dispositivos autenticadores;</li>
          <li>utilização indevida da conta.</li>
        </ul>

        <p>
          A CTech poderá bloquear preventivamente a conta até a conclusão das
          verificações de segurança necessárias.
        </p>
      </LegalSection>

      <LegalSection heading="5. Verificação de identidade (KYC)">
        <p>
          Alguns serviços podem exigir procedimentos adicionais de verificação
          de identidade, incluindo envio de documentos, comprovantes de
          endereço, selfies ou outras informações necessárias para prevenção
          de fraudes e cumprimento de obrigações legais.
        </p>

        <p>
          A recusa em fornecer informações necessárias poderá impedir o acesso
          a determinadas funcionalidades.
        </p>
      </LegalSection>

      <LegalSection heading="6. Uso aceitável">
        <p>É proibido:</p>

        <ul className="list-disc pl-5 space-y-2">
          <li>utilizar a plataforma para fins ilícitos;</li>
          <li>praticar fraude ou tentativa de fraude;</li>
          <li>utilizar contas de terceiros;</li>
          <li>fornecer informações falsas;</li>
          <li>realizar engenharia reversa;</li>
          <li>realizar scraping sem autorização;</li>
          <li>executar testes de intrusão não autorizados;</li>
          <li>interferir na disponibilidade dos serviços;</li>
          <li>
            utilizar APIs de forma abusiva ou em desacordo com sua finalidade;
          </li>
          <li>violar direitos de terceiros.</li>
        </ul>
      </LegalSection>

      <LegalSection heading="7. APIs e integrações">
        <p>
          A CTech poderá disponibilizar APIs, integrações OAuth 2.0 e OpenID
          Connect para utilização por terceiros.
        </p>

        <p>
          O uso dessas integrações poderá estar sujeito a requisitos técnicos,
          limites operacionais, documentação própria e termos adicionais.
        </p>

        <p>
          A CTech poderá suspender credenciais de API em caso de uso abusivo,
          comprometimento de segurança ou descumprimento destes Termos.
        </p>
      </LegalSection>

      <LegalSection heading="8. Propriedade intelectual">
        <p>
          Todos os direitos relacionados à plataforma, software, marcas,
          documentação, layout, interfaces e demais elementos pertencem à
          CTech ou aos respectivos licenciantes.
        </p>

        <p>
          Nenhuma disposição destes Termos transfere direitos de propriedade
          intelectual ao usuário.
        </p>
      </LegalSection>

      <LegalSection heading="9. Disponibilidade">
        <p>
          Os serviços são disponibilizados &ldquo;como estão&rdquo; e &ldquo;conforme disponíveis&rdquo;.
        </p>

        <p>
          Embora a CTech adote esforços razoáveis para garantir elevada
          disponibilidade, não há garantia de funcionamento ininterrupto,
          livre de erros ou imune a falhas de terceiros.
        </p>
      </LegalSection>

      <LegalSection heading="10. Registros e auditoria">
        <p>
          A CTech poderá manter registros de acesso, eventos de segurança,
          logs operacionais e registros de auditoria pelo prazo considerado
          necessário para:
        </p>

        <ul className="list-disc pl-5 space-y-2">
          <li>prevenção de fraudes;</li>
          <li>investigações internas;</li>
          <li>cumprimento de obrigações legais;</li>
          <li>resolução de disputas;</li>
          <li>proteção dos direitos da CTech e de terceiros.</li>
        </ul>

        <p>
          Os registros poderão incluir endereço IP, navegador, dispositivo,
          horários de acesso e demais metadados técnicos.
        </p>
      </LegalSection>

      <LegalSection heading="11. Suspensão e encerramento">
        <p>
          A CTech poderá suspender ou encerrar contas quando houver indícios
          razoáveis de:
        </p>

        <ul className="list-disc pl-5 space-y-2">
          <li>fraude;</li>
          <li>violação destes Termos;</li>
          <li>uso indevido da plataforma;</li>
          <li>determinação judicial ou administrativa;</li>
          <li>risco à segurança dos serviços.</li>
        </ul>

        <p>
          Sempre que possível, o usuário será previamente notificado.
        </p>
      </LegalSection>

      <LegalSection heading="12. Limitação de responsabilidade">
        <p>
          Na máxima extensão permitida pela legislação brasileira, a CTech não
          será responsável por:
        </p>

        <ul className="list-disc pl-5 space-y-2">
          <li>lucros cessantes;</li>
          <li>danos indiretos;</li>
          <li>indisponibilidade de serviços de terceiros;</li>
          <li>falhas de provedores de internet;</li>
          <li>atos de terceiros ou força maior.</li>
        </ul>

        <p>
          Nada nestes Termos limita direitos indisponíveis assegurados pela
          legislação consumerista brasileira.
        </p>
      </LegalSection>

      <LegalSection heading="13. Alterações destes Termos">
        <p>
          Estes Termos poderão ser atualizados periodicamente.
        </p>

        <p>
          Alterações relevantes serão comunicadas previamente por e-mail,
          notificação na plataforma ou outro meio adequado.
        </p>

        <p>
          O uso continuado dos serviços após a entrada em vigor das alterações
          constituirá aceitação da nova versão.
        </p>
      </LegalSection>

      <LegalSection heading="14. Lei aplicável e foro">
        <p>
          Estes Termos são regidos pelas leis da República Federativa do
          Brasil.
        </p>

        <p>
          Fica eleito o foro da Comarca de Teresina/PI, ressalvadas as regras
          de competência previstas na legislação consumerista aplicável.
        </p>
      </LegalSection>

      <LegalSection heading="15. Contato">
        <p>
          Dúvidas poderão ser encaminhadas para:
        </p>

        <ul className="list-disc pl-5 space-y-2">
          <li>dpo@aoctech.app</li>
          <li>legal@aoctech.app</li>
          <li>(86) 9 8803-3430</li>
        </ul>

        <p>
          Encarregado pelo tratamento de dados:
          <strong> Artur Oliveira Carvalho</strong>.
        </p>
      </LegalSection>
    </LegalPageLayout>
  )
}
