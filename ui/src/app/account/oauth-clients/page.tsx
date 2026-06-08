import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { AppWindow } from 'lucide-react'

export default function OAuthClientsPage() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">OAuth Clients</h1>
        <p className="text-muted-foreground text-sm mt-1">
          Applications authorized to act on your behalf.
        </p>
      </div>

      <Card>
        <CardHeader>
          <div className="flex items-center gap-2">
            <AppWindow className="size-5" />
            <CardTitle className="text-base">Client management</CardTitle>
            <Badge variant="secondary" className="text-xs">Coming soon</Badge>
          </div>
          <CardDescription>
            OAuth client registration and management will be available in a future release.
            Contact the administrator to register a new application.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-muted-foreground">
            If you need to integrate an application with arturocarvalho.com&apos;s identity provider,
            reach out via the contact page to get a client ID and secret provisioned.
          </p>
        </CardContent>
      </Card>
    </div>
  )
}
