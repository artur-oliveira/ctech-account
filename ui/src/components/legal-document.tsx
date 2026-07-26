import Link from 'next/link'
import {LegalPageLayout, LegalSection,} from '@/components/legal-page-layout'
import {type LegalDocumentId, legalDocuments} from '@/lib/legal-documents'

export function LegalDocumentPage({documentId}: { documentId: LegalDocumentId }) {
  const document = legalDocuments[documentId]
  const versionHistory = document.versions;

  return (
    <LegalPageLayout
      title={document.title}
      version={document.version}
      updatedAt={document.updatedAt}
      versionHistory={versionHistory}
    >
      {document.intro && <p>{document.intro}</p>}
      {document.sections.map((section) => (
        <LegalSection key={section.heading} heading={section.heading}>
          {section.paragraphs.map((paragraph) => <p key={paragraph}>{paragraph}</p>)}
          {section.items && (
            <ul className="list-disc space-y-2 pl-5">
              {section.items.map((item) => <li key={item}>{item}</li>)}
            </ul>
          )}
        </LegalSection>
      ))}
      <p className="border-t pt-6 text-xs text-muted-foreground">
        Este documento integra a <Link href="/legal" className="underline underline-offset-4">Central Jurídica da
        CTech</Link>.
      </p>
    </LegalPageLayout>
  )
}
