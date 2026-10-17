'use client'

import {Suspense} from 'react'
import {useSearchParams} from 'next/navigation'
import {DeletionLinkPage} from '@/components/deletion-link-page'
import {confirmDeletionAPI} from '@/lib/mutations'

function ConfirmFromLink() {
  const params = useSearchParams()
  return <DeletionLinkPage kind="confirm" requestID={params.get('request') ?? ''} token={params.get('token') ?? ''} service={params.get('service') ?? undefined} action={confirmDeletionAPI}/>
}

export default function ConfirmDeletionPage() {
  return <Suspense fallback={null}><ConfirmFromLink/></Suspense>
}
