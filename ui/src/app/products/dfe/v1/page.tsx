import type {Metadata} from 'next'
import {LegalDocumentPage} from '@/components/legal-document'

export const metadata: Metadata = {title: 'Termos do CTech DF-e — versão 1.0'}

export default function Page() {
  return <LegalDocumentPage documentId="dfe-v1" />
}
