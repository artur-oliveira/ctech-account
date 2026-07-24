import type {Metadata} from 'next'
import {LegalDocumentPage} from '@/components/legal-document'
export const metadata: Metadata = {title: 'Divulgação Responsável'}
export default function Page() { return <LegalDocumentPage documentId="responsible-disclosure"/> }
