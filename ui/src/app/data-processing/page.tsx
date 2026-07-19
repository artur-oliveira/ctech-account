import type {Metadata} from 'next'
import {LegalDocumentPage} from '@/components/legal-document'
export const metadata: Metadata = {title: 'Data Processing Addendum'}
export default function Page() { return <LegalDocumentPage documentId="data-processing"/> }
