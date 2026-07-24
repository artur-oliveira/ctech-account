import type {Metadata} from 'next'
import {LegalDocumentPage} from '@/components/legal-document'
export const metadata: Metadata = {title: 'Política de Segurança'}
export default function Page() { return <LegalDocumentPage documentId="security"/> }
