import type {Metadata} from 'next'
import {LegalDocumentPage} from '@/components/legal-document'
export const metadata: Metadata = {title: 'Política de KYC'}
export default function Page() { return <LegalDocumentPage documentId="kyc"/> }
