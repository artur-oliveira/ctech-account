import type {Metadata} from 'next'
import {LegalDocumentPage} from '@/components/legal-document'
export const metadata: Metadata = {title: 'Termos da CTech Wallet'}
export default function Page() { return <LegalDocumentPage documentId="wallet"/> }
