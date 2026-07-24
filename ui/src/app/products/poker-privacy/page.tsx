import type {Metadata} from 'next'
import {LegalDocumentPage} from '@/components/legal-document'

export const metadata: Metadata = {title: 'Privacidade do CTech Poker'}

export default function Page() {
  return <LegalDocumentPage documentId="poker-privacy"/>
}
