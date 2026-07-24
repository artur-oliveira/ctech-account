import type {Metadata} from 'next'
import {LegalDocumentPage} from '@/components/legal-document'
export const metadata: Metadata = {title: 'Termos do CTech DF-e'}
export default function Page() { return <LegalDocumentPage documentId="dfe"/> }
