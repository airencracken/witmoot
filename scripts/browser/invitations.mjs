// SPDX-License-Identifier: AGPL-3.0-or-later
import { execFileSync } from 'node:child_process';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';

const moduleDirectory = execFileSync('go', ['list', '-m', '-f', '{{.Dir}}', 'github.com/airencracken/comfylib'], { encoding: 'utf8' }).trim();
export const { checkInvitations } = await import(pathToFileURL(join(moduleDirectory, 'ui/uitest/invitations.mjs')));
