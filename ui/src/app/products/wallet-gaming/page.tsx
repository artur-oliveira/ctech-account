import type {Metadata} from 'next'
import {LegalDocumentPage} from '@/components/legal-document'
export const metadata: Metadata = {title: 'Termos da Wallet para Jogos'}
export default function Page() { return <LegalDocumentPage documentId="wallet-gaming"/> }
