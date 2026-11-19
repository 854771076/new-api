/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'

export function CliDocsDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const server = window.location.origin
  const shellServer = `'${server.replaceAll("'", "'\\''")}'`
  const psServer = `'${server.replaceAll("'", "''")}'`
  const commands = [
    {
      label: t('Windows PowerShell'),
      value: `$server = ${psServer}\n$installer = Join-Path $env:TEMP ([Guid]::NewGuid().ToString() + '.ps1')\nInvoke-WebRequest -UseBasicParsing -TimeoutSec 180 -MaximumRedirection 0 "$server/api/cli/install.ps1" -OutFile $installer\npowershell -NoProfile -ExecutionPolicy Bypass -File $installer -Server $server`,
    },
    {
      label: t('macOS / Linux'),
      value: `server=${shellServer}\ninstaller=$(mktemp)\ncurl --fail --proto '=https' "$server/api/cli/install.sh" -o "$installer" && sh "$installer" "$server"\nrm -f "$installer"\nexport PATH="$HOME/.newapi-cli/bin:$PATH"`,
    },
    {
      label: t('Login and usage'),
      value: `newapi-cli auth login --server ${shellServer}\nnewapi-cli auth status\nnewapi-cli user list\nnewapi-cli --help`,
    },
    {
      label: t('Update CLI and skills'),
      value: `newapi-cli version --server ${shellServer}\nnewapi-cli self-update --run --server ${shellServer}`,
    },
  ]

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('CLI installation & usage')}
      description={t(
        'Install the CLI and both skills directly from this server.'
      )}
    >
      <div className='space-y-5'>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Use a trusted HTTPS site. Installers verify downloads with SHA-256. npm is no longer used.'
          )}
        </p>
        {commands.map((command) => (
          <section key={command.label} className='space-y-2'>
            <div className='flex items-center justify-between gap-2'>
              <h3 className='text-sm font-medium'>{command.label}</h3>
              <CopyButton
                value={command.value}
                aria-label={t('Copy {{name}} commands', {
                  name: command.label,
                })}
              />
            </div>
            <pre className='bg-muted overflow-x-auto rounded-md p-3 text-xs leading-relaxed'>
              <code>{command.value}</code>
            </pre>
          </section>
        ))}
        <p className='text-muted-foreground text-sm'>
          {t(
            'CLI: ~/.newapi-cli/bin. Skills: ~/.agents/skills/newapi-cli and ~/.agents/skills/new-api-app-framework. Updates replace both skill directories and preserve login profiles.'
          )}
        </p>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Restart your terminal after Windows installation. On macOS/Linux, add ~/.newapi-cli/bin to your shell PATH. If an older npm CLI takes precedence, remove only the old newapi-cli npm package after verifying the native installation.'
          )}
        </p>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Login opens browser authorization. Approve only the required permissions, then paste the one-time code into the CLI. Keep PATs and API keys private.'
          )}
        </p>
      </div>
    </Dialog>
  )
}
