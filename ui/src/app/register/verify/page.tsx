import Link from 'next/link'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'

export default function VerifyEmailPage() {
  return (
    <div className="min-h-screen flex items-center justify-center bg-muted/40 p-4">
      <div className="w-full max-w-md">
        <Card>
          <CardHeader>
            <CardTitle>Check your email</CardTitle>
            <CardDescription>We sent a verification link to your email address.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <p className="text-sm text-muted-foreground">
              Click the link in your email to verify your account. If you don&apos;t see it, check
              your spam folder.
            </p>
            <Button render={<Link href="/login" />} variant="outline" className="w-full">
              Back to sign in
            </Button>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
