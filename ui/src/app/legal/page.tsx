import type {Metadata} from 'next'
import Link from 'next/link'
import {legalGroups} from '@/lib/legal-documents'

export const metadata: Metadata = {
  title: 'Central Jurídica',
  description: 'Termos, políticas e documentos de conformidade da plataforma CTech.',
}

export default function LegalCenterPage() {
  return (
    <main className="min-h-screen bg-muted/40">
      <div className="mx-auto max-w-5xl px-4 py-12">
        <Link href="/" className="text-sm text-muted-foreground underline underline-offset-4 hover:text-foreground">
          ← CTech Account
        </Link>
        <div className="mt-6 max-w-2xl space-y-3">
          <p className="text-sm font-medium text-primary">CTech Legal Center</p>
          <h1 className="text-3xl font-semibold tracking-tight">Central Jurídica</h1>
          <p className="text-muted-foreground">
            Os documentos que regem a conta, a privacidade, as integrações e os produtos do ecossistema CTech, reunidos em um só lugar.
          </p>
        </div>

        <div className="mt-10 space-y-10">
          {legalGroups.map((group) => (
            <section key={group.title} className="space-y-4">
              <h2 className="text-xl font-semibold">{group.title}</h2>
              <div className="grid gap-3 md:grid-cols-2">
                {group.links.map((document) => (
                  <Link
                    key={document.href}
                    href={document.href}
                    className="rounded-xl border bg-background p-5 transition-colors hover:border-primary/50 hover:bg-accent/40"
                  >
                    <h3 className="font-medium">{document.label}</h3>
                    <p className="mt-1 text-sm text-muted-foreground">{document.description}</p>
                  </Link>
                ))}
              </div>
            </section>
          ))}
        </div>

        <section className="mt-12 rounded-xl border bg-background p-6 text-sm">
          <h2 className="font-semibold">Versionamento</h2>
          <p className="mt-2 text-muted-foreground">
            Cada documento informa sua versão e data de atualização. Versões históricas dos Termos de Uso e da Política de Privacidade permanecem disponíveis em suas páginas.
          </p>
        </section>
      </div>
    </main>
  )
}
