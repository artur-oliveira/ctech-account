export type LegalDocumentId =
  | 'cookies'
  | 'security'
  | 'acceptable-use'
  | 'kyc'
  | 'developer'
  | 'data-processing'
  | 'responsible-disclosure'
  | 'transparency'
  | 'dfe'
  | 'dfe-v1'
  | 'wallet'
  | 'wallet-v1'
  | 'wallet-gaming'
  | 'poker'
  | 'poker-rules'
  | 'billing'

export type LegalSectionData = {
  heading: string
  paragraphs: string[]
  items?: string[]
}

export type LegalDocument = {
  title: string
  description: string
  version: string
  updatedAt: string
  intro: string
  sections: LegalSectionData[]
}

const updatedAt = '19 de julho de 2026'

export const legalDocuments: Record<LegalDocumentId, LegalDocument> = {
  cookies: {
    title: 'Política de Cookies',
    description: 'Como a CTech utiliza cookies e tecnologias estritamente necessárias.',
    version: '1.0',
    updatedAt,
    intro: 'Esta Política complementa a Política de Privacidade e explica as tecnologias armazenadas no dispositivo ao usar a plataforma CTech.',
    sections: [
      {
        heading: '1. O que utilizamos',
        paragraphs: ['Atualmente usamos apenas cookies e armazenamento local estritamente necessários para autenticação, segurança, preferências essenciais e continuidade das sessões. Não usamos cookies publicitários nem vendemos informações de navegação.']
      },
      {
        heading: '2. Cookies de autenticação',
        paragraphs: ['Os cookies ctech_session e ctech_rt são protegidos contra acesso por JavaScript e mantêm, respectivamente, a sessão unificada e a renovação segura de tokens. O marcador ctech_auth informa ao aplicativo apenas que pode existir uma sessão; ele não contém credenciais.'],
        items: ['cookies de sessão e renovação são enviados apenas em conexões seguras;', 'tokens de renovação são rotacionados para reduzir risco de reutilização;', 'preferências de idioma e interface podem ser mantidas no dispositivo.']
      },
      {
        heading: '3. Base legal e escolhas',
        paragraphs: ['Essas tecnologias são indispensáveis à execução do serviço e à prevenção de fraude, por isso não dependem de consentimento. Bloqueá-las no navegador poderá impedir login, recuperação de sessão e recursos protegidos.']
      },
      {
        heading: '4. Novas categorias',
        paragraphs: ['Se analytics, personalização não essencial ou publicidade forem adotados, esta Política será atualizada e, quando exigido, a escolha será solicitada antes da ativação.']
      },
      {heading: '5. Contato', paragraphs: ['Dúvidas e solicitações podem ser enviadas para dpo@aoctech.app.']},
    ],
  },
  security: {
    title: 'Política de Segurança',
    description: 'Princípios de segurança aplicáveis à plataforma CTech.',
    version: '1.0',
    updatedAt,
    intro: 'A CTech mantém controles técnicos e administrativos proporcionais aos riscos de uma plataforma de identidade, APIs, documentos e serviços financeiros integrados.',
    sections: [
      {
        heading: '1. Controles de identidade',
        paragraphs: ['Senhas são protegidas com Argon2id. A plataforma oferece MFA, TOTP e passkeys WebAuthn, sessões revogáveis, rotação de tokens e detecção de reutilização.']
      },
      {
        heading: '2. Proteção da infraestrutura',
        paragraphs: ['A infraestrutura utiliza AWS e Cloudflare, criptografia TLS em trânsito, criptografia gerenciada para dados sensíveis, privilégios mínimos, segregação lógica entre ambientes, rate limiting e registros de auditoria.']
      },
      {
        heading: '3. Disponibilidade e recuperação',
        paragraphs: ['São mantidos backups e recuperação pontual quando suportados pelo serviço, além de monitoramento e procedimentos de resposta. Nenhum sistema é absolutamente imune a falhas e os controles são revistos conforme o risco evolui.']
      },
      {
        heading: '4. Responsabilidade do usuário',
        paragraphs: ['O usuário deve proteger dispositivos e fatores de autenticação, usar credenciais exclusivas, revisar sessões ativas e comunicar imediatamente qualquer suspeita de comprometimento.']
      },
      {
        heading: '5. Incidentes',
        paragraphs: ['Eventos são investigados, contidos e documentados. Incidentes com risco ou dano relevante serão comunicados à ANPD e aos titulares nos termos aplicáveis.']
      },
    ],
  },
  'acceptable-use': {
    title: 'Política de Uso Aceitável',
    description: 'Regras contra abuso da plataforma, contas e APIs.',
    version: '1.0',
    updatedAt,
    intro: 'Esta Política aplica-se a contas, clientes OAuth, chaves de API, integrações e todos os produtos CTech.',
    sections: [
      {
        heading: '1. Condutas proibidas',
        paragraphs: ['É proibido usar os serviços para atividade ilícita, fraudulenta, enganosa ou capaz de prejudicar usuários, terceiros ou a infraestrutura.'],
        items: ['malware, phishing, spam, engenharia social ou distribuição de conteúdo ilícito;', 'brute force, exploração de vulnerabilidades, testes sem autorização ou evasão de controles;', 'scraping abusivo, cryptojacking, mineração, proxy ou VPN oferecidos de modo abusivo;', 'fraude de identidade, múltiplas contas para evasão, manipulação ou uso de credenciais de terceiros;', 'violação de propriedade intelectual, privacidade, sigilo ou direitos de terceiros.']
      },
      {
        heading: '2. Proteção das APIs',
        paragraphs: ['É proibido contornar limites, compartilhar segredos, adulterar fluxos OAuth/OIDC, induzir consentimento enganoso ou solicitar escopos incompatíveis com a finalidade declarada.']
      },
      {
        heading: '3. Investigação e medidas',
        paragraphs: ['A CTech poderá preservar evidências, limitar tráfego, revogar credenciais, suspender funcionalidades ou encerrar contas de forma proporcional ao risco. Sempre que seguro e possível, haverá notificação e oportunidade de esclarecimento.']
      },
      {
        heading: '4. Denúncias',
        paragraphs: ['Abusos podem ser comunicados a legal@aoctech.app. Vulnerabilidades devem seguir a Política de Divulgação Responsável.']
      },
    ],
  },
  kyc: {
    title: 'Política de Verificação de Identidade (KYC)',
    description: 'Critérios, revisão e proteção de dados da verificação de identidade.',
    version: '1.0',
    updatedAt,
    intro: 'A verificação de identidade reduz fraude e pode ser exigida para funcionalidades de maior risco, especialmente operações da Wallet e produtos com dinheiro real.',
    sections: [
      {
        heading: '1. Informações solicitadas',
        paragraphs: ['A CTech poderá solicitar CPF, nome civil, data de nascimento, endereço, frente e verso de documento oficial e capturas de selfie orientadas para prova de presença. Documentos devem ser legíveis, válidos e pertencer ao titular da conta.']
      },
      {
        heading: '2. Análise humana',
        paragraphs: ['A decisão de aprovação ou rejeição é realizada por revisor autorizado. Atualmente não há reconhecimento facial automatizado nem decisão exclusivamente automatizada. Poderão ser solicitados reenvio ou esclarecimentos quando o material for incompleto, inconsistente ou suspeito.']
      },
      {
        heading: '3. Resultado e revisão',
        paragraphs: ['A verificação pode ficar pendente, ser aprovada, expirar ou ser rejeitada. A CTech não revela sinais antifraude cuja divulgação prejudique os controles, mas o titular pode solicitar revisão por dpo@aoctech.app.']
      },
      {
        heading: '4. Tratamento e retenção',
        paragraphs: ['Dados são tratados para execução contratual, prevenção à fraude, exercício regular de direitos e obrigações aplicáveis, conforme a natureza do dado e da operação. Capturas biométricas são acessadas apenas por pessoal autorizado e eliminadas após a decisão quando não houver fundamento para conservação. Registros mínimos podem ser preservados para fraude, auditoria e defesa de direitos.']
      },
      {
        heading: '5. Segurança e compartilhamento',
        paragraphs: ['Documentos são armazenados em infraestrutura protegida e não são públicos. O compartilhamento limita-se a prestadores essenciais, instituições parceiras e autoridades quando houver fundamento jurídico.']
      },
    ],
  },
  developer: {
    title: 'Contrato para Desenvolvedores',
    description: 'Termos para APIs, OAuth 2.0, OpenID Connect e integrações CTech.',
    version: '1.0',
    updatedAt,
    intro: 'Ao registrar um cliente OAuth, criar uma chave de API ou integrar uma aplicação, o desenvolvedor aceita este Contrato e os demais documentos da Central Jurídica.',
    sections: [
      {
        heading: '1. Registro e credenciais',
        paragraphs: ['Informações do aplicativo e URIs de redirecionamento devem ser exatas. Segredos, chaves e tokens são confidenciais, não podem ser incorporados em clientes públicos nem compartilhados, e devem ser rotacionados após suspeita de exposição.']
      },
      {
        heading: '2. Escopos e consentimento',
        paragraphs: ['A aplicação solicitará apenas os escopos necessários, explicará sua finalidade e respeitará recusas e revogações. É proibido esconder, falsificar ou manipular telas de consentimento.']
      },
      {
        heading: '3. Dados e segurança',
        paragraphs: ['Dados recebidos serão usados somente para a finalidade informada, protegidos por medidas adequadas e eliminados quando deixarem de ser necessários ou quando o acesso for revogado, ressalvadas obrigações legais. Incidentes relevantes devem ser informados sem demora indevida.']
      },
      {
        heading: '4. Limites e compatibilidade',
        paragraphs: ['O desenvolvedor respeitará documentação, rate limits, versões e políticas de uso. A CTech poderá alterar ou descontinuar APIs mediante aviso razoável quando possível e agir imediatamente diante de risco de segurança.']
      },
      {
        heading: '5. Auditoria e suspensão',
        paragraphs: ['A CTech poderá solicitar evidências de conformidade e suspender integrações que criem risco, violem direitos, excedam autorização ou descumpram este Contrato.']
      },
    ],
  },
  'data-processing': {
    title: 'Aditivo de Tratamento de Dados (DPA)',
    description: 'Condições B2B para tratamento de dados pessoais por conta de clientes.',
    version: '1.0',
    updatedAt,
    intro: 'Este Aditivo integra o contrato entre o cliente empresarial, na qualidade de Controlador, e A O CARVALHO TECH, como Operadora, quando a CTech trata dados pessoais por instrução do cliente.',
    sections: [
      {
        heading: '1. Objeto e instruções',
        paragraphs: ['A Operadora tratará os dados apenas para prestar os serviços contratados e conforme instruções documentadas, salvo obrigação legal. O Controlador responde pela licitude, transparência, qualidade e base legal da coleta.']
      },
      {
        heading: '2. Pessoas, dados e finalidade',
        paragraphs: ['O tratamento pode abranger dados cadastrais, fiscais, técnicos e transacionais de usuários, clientes, fornecedores e destinatários, pelo prazo do serviço e da retenção contratual ou legal aplicável.']
      },
      {
        heading: '3. Confidencialidade e segurança',
        paragraphs: ['Pessoas autorizadas estarão sujeitas a confidencialidade. A Operadora manterá controles de acesso, criptografia em trânsito, registros, backups e resposta a incidentes proporcionais ao risco.']
      },
      {
        heading: '4. Suboperadores e transferência',
        paragraphs: ['O Controlador autoriza suboperadores necessários, incluindo provedores globais de infraestrutura. A CTech permanece responsável por impor obrigações compatíveis e adotar mecanismos válidos para transferências internacionais.']
      },
      {
        heading: '5. Direitos e incidentes',
        paragraphs: ['A Operadora prestará assistência razoável para solicitações de titulares, avaliações e comunicações. Incidentes confirmados relacionados aos dados do cliente serão comunicados sem demora indevida, com informações disponíveis para a resposta.']
      },
      {
        heading: '6. Término e auditoria',
        paragraphs: ['Ao término, dados serão devolvidos ou eliminados conforme recursos do serviço, ressalvadas retenções obrigatórias. Auditorias devem ser razoáveis, proteger a segurança de outros clientes e priorizar relatórios e evidências existentes.']
      },
    ],
  },
  'responsible-disclosure': {
    title: 'Política de Divulgação Responsável',
    description: 'Canal e safe harbor para relatos de vulnerabilidades.',
    version: '1.0',
    updatedAt,
    intro: 'Pesquisadores de boa-fé ajudam a proteger a CTech. Relatos devem ser enviados para security@aoctech.app com passos de reprodução e impacto observado.',
    sections: [
      {
        heading: '1. Escopo',
        paragraphs: ['Estão no escopo ativos públicos sob aoctech.app operados pela CTech. Serviços de terceiros, engenharia social, ataques físicos e testes destrutivos ficam fora do escopo.']
      },
      {
        heading: '2. Regras de pesquisa',
        paragraphs: ['Use apenas contas e dados próprios, minimize acesso, não persista, altere ou exfiltre dados, não cause indisponibilidade e interrompa o teste ao encontrar informação de terceiros.']
      },
      {
        heading: '3. Safe harbor',
        paragraphs: ['A CTech não buscará ação contra pesquisa que respeite esta Política, seja proporcional, de boa-fé e prontamente comunicada. Isso não autoriza violação de direitos de terceiros nem garante recompensa.']
      },
      {
        heading: '4. Resposta e divulgação',
        paragraphs: ['Buscaremos acusar recebimento, avaliar severidade e manter o pesquisador informado. A divulgação pública deve aguardar correção coordenada ou autorização expressa.']
      },
    ],
  },
  transparency: {
    title: 'Relatório de Transparência',
    description: 'Indicadores públicos de privacidade, segurança e governança.',
    version: '2026.1',
    updatedAt,
    intro: 'Este primeiro relatório estabelece a metodologia pública da CTech. Os números cobrem 2026 e serão atualizados após o encerramento do período, sem inventar estatísticas ainda não consolidadas.',
    sections: [
      {
        heading: 'Período de 2026',
        paragraphs: ['Solicitações LGPD: aguardando consolidação anual.', 'Contas removidas após solicitação: aguardando consolidação anual.', 'Ordens judiciais e requisições governamentais: aguardando consolidação anual.', 'Incidentes de segurança comunicáveis: aguardando consolidação anual.']
      },
      {
        heading: 'Metodologia',
        paragraphs: ['São contabilizadas solicitações concluídas e eventos confirmados no período. Informações que revelem titulares, investigações, controles antifraude ou segredo legal não são publicadas.']
      },
      {
        heading: 'Mudanças relevantes',
        paragraphs: ['Em julho de 2026, a documentação jurídica foi centralizada no CTech Account e organizada em documentos gerais, empresariais e específicos de produto.']
      },
    ],
  },
  dfe: {
    title: 'Termos Adicionais — CTech DF-e',
    description: 'Condições específicas do serviço de documentos fiscais eletrônicos.',
    version: '2.0',
    updatedAt,
    intro: 'Estes Termos complementam os Termos de Uso, a Política de Privacidade e o DPA da CTech.',
    sections: [
      {
        heading: '1. Serviço e responsabilidade fiscal',
        paragraphs: ['O DF-e permite emitir, transmitir, consultar e armazenar NF-e, NFC-e, CT-e, MDF-e e documentos futuramente suportados. O cliente responde pela exatidão, classificação, tributação, prazos e legitimidade das operações; a CTech não presta consultoria contábil ou tributária.']
      },
      {
        heading: '2. Organizações e dados de terceiros',
        paragraphs: ['O cliente controla usuários de sua organização e os dados de clientes, fornecedores, transportadores e destinatários. Para esses dados, o cliente é Controlador e a CTech é Operadora nos termos do DPA.']
      },
      {
        heading: '3. Certificado digital',
        paragraphs: ['O cliente garante titularidade, validade, senha, poderes e autorização de uso do certificado A1. A CTech o protege por criptografia e acesso restrito aos componentes de emissão, sem assumir responsabilidade por expiração, revogação ou uso autorizado pelo cliente.']
      },
      {
        heading: '4. SEFAZ, contingência e eventos',
        paragraphs: ['Autorização, cancelamento, inutilização, carta de correção, manifestação, EPEC e contingências dependem de regras e sistemas públicos. O cliente deve acompanhar retornos e prazos; indisponibilidade governamental não é controlada pela CTech.']
      },
      {
        heading: '5. Guarda e exportação',
        paragraphs: ['XML e representações ficam disponíveis conforme o plano e a legislação. O cliente deve manter cópias e exportar dados antes do encerramento, sem prejuízo da retenção legal aplicável.']
      },
    ],
  },
  'dfe-v1': {
    title: 'Termos Adicionais — CTech DF-e',
    description: 'Versão histórica dos termos específicos do CTech DF-e.',
    version: '1.0',
    updatedAt: '10 de julho de 2026',
    intro: '',
    sections: [
      {heading: '1. Sobre este documento', paragraphs: ['Este documento complementa os Termos de Uso e a Política de Privacidade gerais da CTech, que você já aceitou ao criar sua conta. Ele descreve regras específicas do CTech DFe — a emissão e gestão de notas fiscais eletrônicas (NF-e, NFC-e, CT-e, MDF-e).']},
      {heading: '2. Dados de terceiros nas suas notas', paragraphs: ['Para emitir uma nota fiscal, você informa dados de outras pessoas ou empresas — seus clientes, fornecedores ou destinatários (nome, CPF/CNPJ, endereço). Você é responsável por ter uma base legal válida para tratar esses dados (normalmente, a própria relação comercial) e por garantir que as informações estão corretas. O CTech DFe trata esses dados apenas para gerar e transmitir o documento fiscal correspondente.']},
      {heading: '3. Certificado digital', paragraphs: ['O certificado digital (A1) que você envia é usado exclusivamente para assinar os documentos fiscais da sua própria empresa. Ele é armazenado de forma criptografada e nunca é compartilhado com outra organização dentro da plataforma.']},
      {heading: '4. Envio para a SEFAZ', paragraphs: ['Notas fiscais são, por lei, transmitidas à Secretaria da Fazenda (SEFAZ) do seu estado. Esse envio é obrigatório para que o documento tenha validade fiscal — não é um compartilhamento opcional, é parte do próprio serviço.']},
      {heading: '5. Guarda dos documentos', paragraphs: ['Documentos fiscais autorizados (XML e DANFE) ficam disponíveis para consulta e download na plataforma pelo prazo exigido pela legislação fiscal brasileira (em geral, 5 anos). Após esse período, podem ser arquivados ou removidos.']},
      {heading: '6. Alterações', paragraphs: ['Alterações materiais a este documento exigem um novo aceite antes de continuar usando o CTech DFe. A versão vigente é sempre a publicada nesta página.']},
      {heading: '7. Contato', paragraphs: ['Dúvidas sobre este documento: dpo@aoctech.app.']},
    ],
  },
  wallet: {
    title: 'Termos Adicionais — CTech Wallet',
    description: 'Condições de saldo, Pix, saques e pagamentos internos.',
    version: '2.0',
    updatedAt,
    intro: 'A Wallet é uma carteira interna para pagamentos no ecossistema CTech e operações Pix realizadas por integração com instituição financeira parceira. Não é conta bancária e a CTech não é instituição financeira.',
    sections: [
      {
        heading: '1. Saldo e parceiro financeiro',
        paragraphs: ['Recursos correspondentes aos saldos são mantidos na estrutura bancária operacional da CTech junto ao Banco Inter, com segregação lógica por usuário. O saldo não constitui depósito bancário, investimento, crédito ou rendimento.']
      },
      {
        heading: '2. Depósitos e conciliação',
        paragraphs: ['Depósitos por Pix dependem de identificação e confirmação do parceiro. Valores com divergência, suspeita ou devolução podem permanecer indisponíveis durante conciliação e análise antifraude.']
      },
      {
        heading: '3. Pagamentos e saques',
        paragraphs: ['O saldo pode pagar serviços integrados. Saques exigem KYC, chave Pix compatível e saldo suficiente para valor e tarifa. O valor mínimo operacional pode resultar em liquidação líquida de R$ 0,01 quando a tarifa mínima for R$ 1,00. Limites vigentes são exibidos antes da confirmação.']
      },
      {
        heading: '4. MED, devoluções e bloqueios',
        paragraphs: ['Operações podem ser bloqueadas, devolvidas ou ajustadas em razão do Mecanismo Especial de Devolução, ordem legal, erro, fraude, duplicidade ou obrigação do parceiro. A CTech preservará evidências e informará o usuário quando permitido.']
      },
      {
        heading: '5. Encerramento',
        paragraphs: ['Antes do encerramento, saldo disponível deve ser usado ou sacado. Valores contestados, bloqueados ou sujeitos a retenção permanecem indisponíveis até resolução.']
      },
    ],
  },
  'wallet-v1': {
    title: 'Termos Adicionais — CTech Wallet',
    description: 'Versão histórica dos termos específicos da CTech Wallet.',
    version: '1.0',
    updatedAt: '11 de julho de 2026',
    intro: 'Este aditivo complementa — e não substitui — os Termos de Uso e a Política de Privacidade da CTech. No que for específico da carteira digital, este aditivo prevalece.',
    sections: [
      {heading: '1. O que é a CTech Wallet', paragraphs: ['A CTech Wallet mantém dois saldos separados na sua conta: o saldo real, movimentado por PIX, e os créditos sandbox, uma moeda virtual usada em aplicações integradas.']},
      {heading: '2. Quem pode usar', paragraphs: ['Para movimentar saldo real você precisa ter 18 anos ou mais e concluir a verificação de identidade da sua conta CTech. Para sacar, a chave PIX de destino precisa pertencer ao mesmo CPF verificado na conta — saques para chaves de terceiros são recusados.']},
      {heading: '3. Depósitos', paragraphs: ['Depósitos são recebidos por PIX. O valor entra na carteira somente após o banco parceiro confirmar o pagamento — nunca apenas com base em uma notificação. Se o CPF de quem pagou for diferente do CPF verificado na sua conta, o depósito é recusado e devolvido automaticamente a quem pagou.']},
      {heading: '4. Saques e taxa', paragraphs: ['Cada saque tem uma taxa, descontada do seu saldo junto com o valor sacado. A taxa padrão é de 2% sobre o valor, com mínimo de R$ 1,00 e máximo de R$ 10,00 por operação, e cobre o custo da transferência PIX. A taxa aplicada à sua carteira é sempre exibida antes de você confirmar o saque.', 'Uma carteira executa uma operação por vez: um novo saque só começa depois que o anterior é concluído.']},
      {heading: '5. Créditos sandbox', paragraphs: ['Créditos sandbox podem ser comprados com saldo real ou concedidos por aplicações integradas. Eles servem para participar de partidas em jogos de habilidade integrados.', 'Créditos sandbox não têm valor monetário, não são resgatáveis e não podem, em nenhuma hipótese, ser convertidos em saldo real nem sacados. A compra de créditos com saldo real é definitiva e não é reembolsável.']},
      {heading: '6. Limites de responsabilidade', paragraphs: ['A CTech Wallet não é uma instituição financeira licenciada pelo Banco Central do Brasil. Ela atua como intermediário técnico de custódia e movimentação de valores via PIX, por meio de um banco parceiro. Não garantimos a disponibilidade ininterrupta da infraestrutura PIX de terceiros e não respondemos por atrasos ou falhas causados por ela.']},
      {heading: '7. Alterações', paragraphs: ['Este aditivo pode ser atualizado. Alterações materiais exigem um novo aceite explícito antes de você continuar usando a carteira.']},
      {heading: '8. Contato', paragraphs: ['A O CARVALHO TECH — CNPJ 62.787.449/0001-07. Encarregado de dados (DPO): dpo@aoctech.app.']},
    ],
  },
  'wallet-gaming': {
    title: 'Termos da Wallet para Jogos',
    description: 'Regras adicionais para uso de saldo em jogos elegíveis.',
    version: '2.0',
    updatedAt,
    intro: 'Este documento complementa os Termos da Wallet para separação e uso de saldo destinado a jogos com dinheiro real ou créditos sandbox.',
    sections: [
      {
        heading: '1. Elegibilidade',
        paragraphs: ['A funcionalidade é exclusiva para maiores de 18 anos com identidade verificada. A CTech pode aplicar limites geográficos, financeiros, de sessão, depósito, perda e pausa.']
      },
      {
        heading: '2. Saldo de jogo e créditos sandbox',
        paragraphs: ['Saldo real e créditos virtuais são contabilizados separadamente. Créditos sandbox não têm valor monetário, não podem ser sacados, vendidos ou convertidos. Transferências entre usuários dependem de funcionalidade expressamente autorizada.']
      },
      {
        heading: '3. Integridade e prevenção à fraude',
        paragraphs: ['São proibidos múltiplas contas, colusão, chip dumping, bots, assistência em tempo real, ghosting, soft play, exploração de falhas, lavagem de dinheiro e ocultação de localização. Operações suspeitas podem ser retidas e investigadas.']
      },
      {
        heading: '4. Jogo responsável',
        paragraphs: ['O usuário deve jogar apenas com recursos que pode perder. A plataforma poderá oferecer autoexclusão, pausas e limites; durante autoexclusão, o acesso a jogos com valor real permanece bloqueado. Apoio pode ser buscado no CVV e em Jogadores Anônimos.']
      },
      {
        heading: '5. Falhas e estornos',
        paragraphs: ['Falhas de conexão, servidor ou regra podem levar a cancelamento, rollback ou recomposição com base nos registros auditáveis. Ganhos resultantes de erro, manipulação ou exploração não são devidos.']
      },
    ],
  },
  poker: {
    title: 'Termos Adicionais — CTech Poker',
    description: 'Condições específicas para mesas de poker online.',
    version: '1.0',
    updatedAt,
    intro: 'O CTech Poker oferece mesas públicas ou privadas de jogo de habilidade, com créditos sandbox e, quando habilitado e permitido, valores reais por meio da Wallet.',
    sections: [
      {
        heading: '1. Elegibilidade e acesso',
        paragraphs: ['Acesso real exige maioridade, KYC e localização permitida. O usuário é responsável por verificar a legalidade em sua jurisdição e não pode usar VPN ou mecanismo para contornar restrições.']
      },
      {
        heading: '2. Mesas e resultados',
        paragraphs: ['As regras publicadas, blinds, limites, buy-in, premiação e eventuais tarifas exibidas antes da entrada integram cada partida. Os registros do servidor prevalecem para reconstruir mãos e resolver divergências.']
      },
      {
        heading: '3. Jogo justo',
        paragraphs: ['São proibidos bots, RTA, colusão, chip dumping, ghosting, compartilhamento de conta, múltiplas contas, exploração de bugs e acesso a cartas ou decisões de terceiros. A CTech pode analisar padrões, preservar logs, anular resultados e suspender envolvidos.']
      },
      {
        heading: '4. Código aberto e propriedade intelectual',
        paragraphs: ['A publicação de código aumenta transparência, mas não autoriza acesso à produção, engenharia social, manipulação de partidas nem uso fora da licença aplicável. Segredos, controles antifraude e infraestrutura podem permanecer privados.']
      },
      {
        heading: '5. Dinheiro real e responsabilidade',
        paragraphs: ['Valores são movimentados pela Wallet. O usuário responde por tributos pessoais e reconhece o risco de perda. Não há garantia de ganho; autoexclusão e limites devem ser respeitados.']
      },
    ],
  },
  'poker-rules': {
    title: 'Regras do CTech Poker',
    description: 'Regras operacionais das partidas de poker.',
    version: '1.0',
    updatedAt,
    intro: 'Estas regras aplicam-se às modalidades e mesas disponibilizadas. A configuração visível da mesa prevalece para blinds, limites, buy-in e tempo de ação.',
    sections: [
      {
        heading: '1. Formação da mão',
        paragraphs: ['As cartas são distribuídas pelo servidor e o pote é atribuído à melhor combinação válida da modalidade. Empates dividem o pote; unidades indivisíveis seguem o critério exibido na mesa.']
      },
      {
        heading: '2. Apostas e potes',
        paragraphs: ['Check, bet, call, raise, fold e all-in obedecem à ordem de ação e ao limite da mesa. Side pots são calculados conforme a contribuição efetiva de cada jogador elegível.']
      },
      {
        heading: '3. Tempo e desconexão',
        paragraphs: ['Expirado o relógio, a ação padrão é aplicada. Desconexão não pausa a mesa; proteções específicas, quando existirem, serão exibidas.']
      },
      {
        heading: '4. Mãos interrompidas',
        paragraphs: ['Quando uma falha impedir conclusão confiável, a mão poderá ser reconstruída pelos registros, cancelada ou revertida ao último estado íntegro. A decisão buscará não beneficiar quem causou ou explorou a falha.']
      },
      {
        heading: '5. Conduta',
        paragraphs: ['É proibida comunicação sobre uma mão ativa, combinação de estratégia entre participantes, assédio, atraso intencional ou qualquer prática de jogo injusto.']
      },
    ],
  },
  billing: {
    title: 'Termos Adicionais — CTech Billing',
    description: 'Condições de assinaturas e cobranças dos serviços CTech.',
    version: '1.0',
    updatedAt,
    intro: 'O Billing gerencia planos, recorrência e cobrança de serviços CTech utilizando saldo da Wallet. Pix Automático ou outros meios somente valerão quando expressamente habilitados.',
    sections: [
      {
        heading: '1. Planos e autorização',
        paragraphs: ['Preço, periodicidade, recursos, impostos e data de cobrança são apresentados na contratação. Ao assinar, o usuário autoriza cobranças recorrentes na Wallet até cancelamento.']
      },
      {
        heading: '2. Falha de cobrança',
        paragraphs: ['Saldo insuficiente ou falha operacional pode gerar novas tentativas, aviso, período de tolerância e suspensão do serviço. O pagamento posterior pode reativar o acesso sem restaurar dados já eliminados após os prazos informados.']
      },
      {
        heading: '3. Alterações e cancelamento',
        paragraphs: ['Upgrade, downgrade e cancelamento produzem efeitos conforme indicado antes da confirmação. O cancelamento impede novas renovações, mas normalmente preserva acesso até o fim do período já pago.']
      },
      {
        heading: '4. Reembolso',
        paragraphs: ['Pedidos são avaliados conforme oferta, uso, falha comprovada e direitos obrigatórios do consumidor. Não são afastados o direito de arrependimento nem outras garantias indisponíveis quando aplicáveis.']
      },
      {
        heading: '5. Tributos e documentos',
        paragraphs: ['Preços incluem ou discriminam tributos conforme aplicável. A emissão de documento fiscal ocorrerá segundo a legislação e a disponibilidade do fluxo implementado.']
      },
    ],
  },
}

export const legalGroups = [
  {
    title: 'Documentos gerais', links: [
      {href: '/terms', label: 'Termos de Uso', description: 'Regras gerais da plataforma.'},
      {
        href: '/privacy',
        label: 'Política de Privacidade',
        description: 'Tratamento de dados pessoais e direitos LGPD.'
      },
      {href: '/cookies', label: 'Política de Cookies', description: legalDocuments.cookies.description},
      {href: '/security-policy', label: 'Política de Segurança', description: legalDocuments.security.description},
      {
        href: '/acceptable-use',
        label: 'Política de Uso Aceitável',
        description: legalDocuments['acceptable-use'].description
      },
      {href: '/kyc-policy', label: 'Política de KYC', description: legalDocuments.kyc.description},
      {
        href: '/developer-agreement',
        label: 'Contrato para Desenvolvedores',
        description: legalDocuments.developer.description
      },
    ]
  },
  {
    title: 'Documentos empresariais', links: [
      {
        href: '/data-processing',
        label: 'Data Processing Addendum',
        description: legalDocuments['data-processing'].description
      },
    ]
  },
  {
    title: 'Produtos', links: [
      {href: '/products/dfe', label: 'CTech DF-e', description: legalDocuments.dfe.description},
      {href: '/products/wallet', label: 'CTech Wallet', description: legalDocuments.wallet.description},
      {
        href: '/products/wallet-gaming',
        label: 'Wallet para Jogos',
        description: legalDocuments['wallet-gaming'].description
      },
      {href: '/products/poker', label: 'CTech Poker', description: legalDocuments.poker.description},
      {href: '/products/poker-rules', label: 'Regras do Poker', description: legalDocuments['poker-rules'].description},
      {href: '/products/billing', label: 'CTech Billing', description: legalDocuments.billing.description},
    ]
  },
  {
    title: 'Confiança e transparência', links: [
      {
        href: '/responsible-disclosure',
        label: 'Divulgação Responsável',
        description: legalDocuments['responsible-disclosure'].description
      },
      {
        href: '/transparency',
        label: 'Relatório de Transparência',
        description: legalDocuments.transparency.description
      },
    ]
  },
]
