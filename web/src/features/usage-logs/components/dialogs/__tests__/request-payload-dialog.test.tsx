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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import type { ReactNode } from 'react'
import { beforeAll, beforeEach, describe, expect, test, vi } from 'vitest'

import { RequestPayloadDialog } from '../request-payload-dialog'

const { getRequestDetailMock } = vi.hoisted(() => ({
  getRequestDetailMock: vi.fn(),
}))

vi.mock('../../../api', () => ({
  getRequestDetail: getRequestDetailMock,
  getRequestMediaBlob: vi.fn(),
}))

vi.mock('@/components/ai-elements/code-block', () => ({
  CodeBlock: (props: { code: string; children?: ReactNode }) => (
    <div>
      <pre>{props.code}</pre>
      {props.children}
    </div>
  ),
  CodeBlockCopyButton: () => null,
}))

function renderDialog(): ReturnType<typeof render> {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <RequestPayloadDialog
        requestId='req-123'
        open
        onOpenChange={() => undefined}
      />
    </QueryClientProvider>
  )
}

describe('request payload dialog', () => {
  beforeAll(() => {
    i18next.addResourceBundle('en', 'translation', {
      'Full Request Log': 'Full Request Log',
      'No content': 'No content',
      Request: 'Request',
      Response: 'Response',
      'Request detail not found': 'Request detail not found',
      'View the complete request and response payload for this call':
        'View the complete request and response payload for this call',
    })
  })

  beforeEach(() => {
    getRequestDetailMock.mockReset()
  })

  test('shows the recorded request and lets the admin switch to the response', async () => {
    getRequestDetailMock.mockResolvedValue({
      success: true,
      data: {
        id: 1,
        request_id: 'req-123',
        user_id: 1,
        created_at: 1,
        endpoint: '/v1/chat/completions',
        model_name: 'gpt-test',
        channel_id: 2,
        token_id: 3,
        status_code: 200,
        is_stream: false,
        request_body: '{"prompt":"hello"}',
        response_body: '{"answer":"world"}',
        request_truncated: false,
        response_truncated: false,
        media: [],
      },
    })

    renderDialog()

    expect(await screen.findByText(/"prompt": "hello"/)).toBeVisible()
    await userEvent.click(screen.getByRole('tab', { name: 'Response' }))
    expect(await screen.findByText(/"answer": "world"/)).toBeVisible()
  })

  test('shows the backend not-found state without rendering empty payload tabs', async () => {
    getRequestDetailMock.mockResolvedValue({
      success: false,
      message: 'Request detail not found',
    })

    renderDialog()

    expect(await screen.findByText('Request detail not found')).toBeVisible()
    expect(
      screen.queryByRole('tab', { name: 'Request' })
    ).not.toBeInTheDocument()
  })
})
