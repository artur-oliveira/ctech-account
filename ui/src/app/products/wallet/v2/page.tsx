import type {Metadata} from 'next'
import {LegalDocumentPage} from '@/components/legal-document'

export const metadata: Metadata = {title: 'Termos da CTech Wallet — versão 2.0'}

export default function Page() {
  return <LegalDocumentPage documentId="wallet-v2"/>
}
