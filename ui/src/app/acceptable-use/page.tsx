import type {Metadata} from 'next'
import {LegalDocumentPage} from '@/components/legal-document'
export const metadata: Metadata = {title: 'Política de Uso Aceitável'}
export default function Page() { return <LegalDocumentPage documentId="acceptable-use"/> }
