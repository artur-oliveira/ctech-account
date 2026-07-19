import type {Metadata} from 'next'
import {LegalDocumentPage} from '@/components/legal-document'
export const metadata: Metadata = {title: 'Contrato para Desenvolvedores'}
export default function Page() { return <LegalDocumentPage documentId="developer"/> }
