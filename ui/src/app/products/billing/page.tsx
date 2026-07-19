import type {Metadata} from 'next'
import {LegalDocumentPage} from '@/components/legal-document'
export const metadata: Metadata = {title: 'Termos do CTech Billing'}
export default function Page() { return <LegalDocumentPage documentId="billing"/> }
