import type { Metadata } from 'next'
import Link from 'next/link'
import { LegalPageLayout, LegalSection, TERMS_VERSION_HISTORY } from '@/components/legal-page-layout'

export const metadata: Metadata = {
  title: 'Termos de Uso',
}

const TOS_VERSION = '1.0'
const UPDATED_AT = '10 de julho de 2026'

export default function TermsPage() {
  return (
    <LegalPageLayout title="Termos de Uso" version={TOS_VERSION} updatedAt={UPDATED_AT} versionHistory={TERMS_VERSION_HISTORY}>

      <LegalSection heading="1. Quem somos">
        <p>
          Estes Termos de Uso regem o acesso e uso da plataforma CTech (&ldquo;Plataforma&rdquo;), operada por{' '}
          <strong>A O CARVALHO TECH</strong>, inscrita no CNPJ sob o nº 62.787.449/0001-07, com sede na Rua Atleta
          Daniel Aragão Matos, 6201, Vale Quem Tem, Teresina/PI (&ldquo;CTech&rdquo;, &ldquo;nós&rdquo;).
        </p>
        <p>
          A Plataforma inclui o serviço de identidade e conta única (CTech Account, em accounts.aoctech.app) e os
          produtos que o utilizam para login e gestão de conta (ex.: CTech DFe, CTech Wallet). Cada produto pode ter
          um aditivo a estes Termos com regras específicas de seu funcionamento; em caso de conflito, o aditivo do
          produto prevalece sobre estes Termos gerais no que for específico daquele produto.
        </p>
      </LegalSection>

      <LegalSection heading="2. Aceite">
        <p>
          Ao criar uma conta na Plataforma, você declara que leu, compreendeu e concorda integralmente com estes
          Termos de Uso e com a nossa{' '}
          <Link href="/privacy" className="underline underline-offset-4">
            Política de Privacidade
          </Link>
          . Se você não concorda, não deve criar uma conta nem utilizar a Plataforma.
        </p>
      </LegalSection>

      <LegalSection heading="3. Elegibilidade e cadastro">
        <ul className="list-disc space-y-2 pl-5">
          <li>É necessário ter 18 (dezoito) anos completos ou mais para criar uma conta.</li>
          <li>
            As informações fornecidas no cadastro (nome, e-mail e, quando aplicável, dados de verificação de
            identidade) devem ser verdadeiras, completas e mantidas atualizadas.
          </li>
          <li>Cada pessoa pode manter apenas uma conta. Contas duplicadas podem ser suspensas ou encerradas.</li>
          <li>
            Você é responsável por manter a confidencialidade de sua senha e dos fatores de autenticação (MFA,
            passkeys) associados à sua conta, e por todas as atividades realizadas a partir dela.
          </li>
        </ul>
      </LegalSection>

      <LegalSection heading="4. Verificação de identidade (KYC)">
        <p>
          Determinados recursos da Plataforma — em particular os que envolvem movimentação de valores financeiros
          (ex.: CTech Wallet) — exigem verificação de identidade (CPF, nome completo e data de nascimento), sujeita a
          idade mínima de 18 anos. Essa verificação é obrigatória para o uso desses recursos e é regida também pelo
          aditivo específico do produto correspondente.
        </p>
      </LegalSection>

      <LegalSection heading="5. Uso aceitável">
        <p>Ao usar a Plataforma, você concorda em não:</p>
        <ul className="list-disc space-y-2 pl-5">
          <li>Utilizar a Plataforma para fins ilícitos, fraudulentos ou que violem direitos de terceiros;</li>
          <li>Tentar contornar controles de segurança, autenticação ou limites de acesso;</li>
          <li>Realizar engenharia reversa, extração automatizada de dados (scraping) ou testes de intrusão sem autorização prévia por escrito;</li>
          <li>Fornecer informações falsas de identidade ou se passar por terceiros;</li>
          <li>Utilizar contas de terceiros sem autorização.</li>
        </ul>
      </LegalSection>

      <LegalSection heading="6. Propriedade intelectual">
        <p>
          Todo o software, marca, layout e conteúdo da Plataforma pertencem à CTech ou a seus licenciantes. Nenhuma
          disposição destes Termos transfere qualquer direito de propriedade intelectual a você, exceto a licença
          limitada, pessoal e não exclusiva de uso da Plataforma conforme aqui previsto.
        </p>
      </LegalSection>

      <LegalSection heading="7. Disponibilidade do serviço">
        <p>
          Envidamos esforços razoáveis para manter a Plataforma disponível e funcionando corretamente, mas não
          garantimos disponibilidade ininterrupta. Manutenções, atualizações ou falhas técnicas podem causar
          indisponibilidade temporária. A Plataforma é fornecida &ldquo;como está&rdquo;, sem garantias implícitas de
          adequação a uma finalidade específica.
        </p>
      </LegalSection>

      <LegalSection heading="8. Limitação de responsabilidade">
        <p>
          Na máxima extensão permitida pela lei brasileira, a CTech não será responsável por danos indiretos,
          incidentais, lucros cessantes ou perda de dados decorrentes do uso ou da impossibilidade de uso da
          Plataforma, exceto nos casos de dolo ou culpa grave. Para produtos que envolvem recursos financeiros
          (ex.: CTech Wallet), condições adicionais de responsabilidade constam no aditivo específico daquele
          produto.
        </p>
      </LegalSection>

      <LegalSection heading="9. Suspensão e encerramento">
        <p>
          Podemos suspender ou encerrar sua conta, a nosso critério e mediante aviso quando possível, em caso de
          violação destes Termos, suspeita de fraude, uso indevido, ordem judicial ou solicitação de autoridade
          competente. Você pode encerrar sua conta a qualquer momento pelas configurações da conta ou entrando em
          contato conosco.
        </p>
      </LegalSection>

      <LegalSection heading="10. Alterações destes Termos">
        <p>
          Podemos alterar estes Termos periodicamente. Alterações materiais serão comunicadas por e-mail ou aviso na
          Plataforma antes de entrarem em vigor. A versão vigente é sempre a publicada nesta página, identificada
          pelo número de versão no topo do documento. O uso continuado da Plataforma após a entrada em vigor de uma
          nova versão constitui aceite dos novos termos.
        </p>
      </LegalSection>

      <LegalSection heading="11. Lei aplicável e foro">
        <p>
          Estes Termos são regidos pelas leis da República Federativa do Brasil. Fica eleito o foro da Comarca de
          Teresina, Estado do Piauí, para dirimir quaisquer controvérsias decorrentes destes Termos, com renúncia a
          qualquer outro, por mais privilegiado que seja, ressalvadas as regras de competência de foro do
          consumidor previstas em lei.
        </p>
      </LegalSection>

      <LegalSection heading="12. Contato">
        <p>
          Dúvidas sobre estes Termos podem ser enviadas para{' '}
          <a href="mailto:dpo@aoctech.app" className="underline underline-offset-4">
            dpo@aoctech.app
          </a>
          .
        </p>
      </LegalSection>
    </LegalPageLayout>
  )
}
