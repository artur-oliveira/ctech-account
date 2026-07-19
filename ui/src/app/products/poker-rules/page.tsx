import type {Metadata} from 'next'
import {LegalDocumentPage} from '@/components/legal-document'
export const metadata: Metadata = {title: 'Regras do CTech Poker'}
export default function Page() { return <LegalDocumentPage documentId="poker-rules"/> }
