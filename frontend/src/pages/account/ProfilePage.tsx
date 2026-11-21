import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { api, errorMessage, fieldErrors } from '../../lib/api'
import { useAuth } from '../../lib/auth'
import { Alert, Button, Card, CardHeader, Input } from '../../components/ui'
import { useToast } from '../../components/toast'

export function ProfilePage() {
  const { me, refreshMe } = useAuth()
  const toast = useToast()
  const [profile, setProfile] = useState({ first_name: me?.user.first_name ?? '', last_name: me?.user.last_name ?? '', phone: me?.user.phone ?? '' })
  const [passwords, setPasswords] = useState({ current_password: '', new_password: '' })

  const saveProfile = useMutation({
    mutationFn: () => api('/auth/me', { method: 'PUT', body: profile }),
    onSuccess: async () => {
      await refreshMe()
      toast.success('Profile updated.')
    },
  })
  const changePassword = useMutation({
    mutationFn: () => api('/auth/change-password', { method: 'POST', body: passwords }),
    onSuccess: () => {
      setPasswords({ current_password: '', new_password: '' })
      toast.success('Password changed. Other devices have been signed out.')
    },
  })
  const pErrors = fieldErrors(saveProfile.error)
  const cErrors = fieldErrors(changePassword.error)

  return (
    <div className="mx-auto max-w-2xl space-y-6 px-4 py-8">
      <h1 className="text-2xl font-semibold tracking-tight">Account settings</h1>

      <Card>
        <CardHeader title="Profile" description={me?.user.email} />
        <form className="space-y-4 p-5" onSubmit={(e) => { e.preventDefault(); saveProfile.mutate() }}>
          {saveProfile.error && !Object.keys(pErrors).length && <Alert tone="error">{errorMessage(saveProfile.error)}</Alert>}
          <div className="grid gap-4 sm:grid-cols-2">
            <Input label="First name" value={profile.first_name} onChange={(e) => setProfile({ ...profile, first_name: e.target.value })} error={pErrors.first_name} required />
            <Input label="Last name" value={profile.last_name} onChange={(e) => setProfile({ ...profile, last_name: e.target.value })} error={pErrors.last_name} required />
          </div>
          <Input label="Mobile number" type="tel" value={profile.phone} onChange={(e) => setProfile({ ...profile, phone: e.target.value })} error={pErrors.phone} />
          <div className="flex justify-end"><Button type="submit" loading={saveProfile.isPending}>Save profile</Button></div>
        </form>
      </Card>

      <Card>
        <CardHeader title="Password" description="Changing your password signs you out everywhere else. Signed up with Google? Use “Forgot your password” on the sign-in page to add one." />
        <form className="space-y-4 p-5" onSubmit={(e) => { e.preventDefault(); changePassword.mutate() }}>
          {changePassword.error && !Object.keys(cErrors).length && <Alert tone="error">{errorMessage(changePassword.error)}</Alert>}
          <Input label="Current password" type="password" autoComplete="current-password" value={passwords.current_password} onChange={(e) => setPasswords({ ...passwords, current_password: e.target.value })} error={cErrors.current_password} required />
          <Input label="New password" type="password" autoComplete="new-password" value={passwords.new_password} onChange={(e) => setPasswords({ ...passwords, new_password: e.target.value })} error={cErrors.new_password} hint="At least 8 characters with a letter and a number." required />
          <div className="flex justify-end"><Button type="submit" loading={changePassword.isPending}>Change password</Button></div>
        </form>
      </Card>
    </div>
  )
}
