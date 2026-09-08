import type {Metadata} from 'next'
import {LegalDocumentPage} from '@/components/legal-document'
export const metadata: Metadata = {title: 'Termos da CTech Ledger'}
export default function Page() { return <LegalDocumentPage documentId="wallet"/> }
