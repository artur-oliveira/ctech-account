'use client'

import {Suspense} from 'react'
import {useSearchParams} from 'next/navigation'
import {DeletionLinkPage} from '@/components/deletion-link-page'
import {cancelDeletionAPI} from '@/lib/mutations'

function CancelFromLink() {
  const params = useSearchParams()
  return <DeletionLinkPage kind="cancel" requestID={params.get('request') ?? ''} token={params.get('token') ?? ''} action={cancelDeletionAPI}/>
}

export default function CancelDeletionPage() {
  return <Suspense fallback={null}><CancelFromLink/></Suspense>
}
