import type {Metadata} from 'next'
import {LegalDocumentPage} from '@/components/legal-document'
export const metadata: Metadata = {title: 'Relatório de Transparência'}
export default function Page() { return <LegalDocumentPage documentId="transparency"/> }
