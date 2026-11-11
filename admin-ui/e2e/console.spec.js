import { expect, test } from '@playwright/test';

test('moderator can sign in, inspect evidence, and resolve a report', async ({ page }) => {
  const report = {
    report_id: 'd1a36e79-b31a-4c11-a9ae-b07181ff22ef',
    reporter_user_id: '9214e80b-7cff-41db-9ff3-1ad463ecfe99',
    target_user_id: 'd721f835-b507-47d1-9755-7a93b8b8e2dd',
    conversation_id: 'conversation-42',
    message_id: 'message-42',
    message_seq: 42,
    reason: 'harassment',
    details: 'Please review this message.',
    message_snapshot: { text: 'You should stop sending me this.' },
    status: 'open',
    resolution_notes: '',
    notification_status: 'sent',
    created_at: '2026-11-10T09:00:00Z',
  };
  const auth = `Basic ${Buffer.from('moderator:correct-horse-battery').toString('base64')}`;
  let reviewedPayload;

  await page.route('**/v1/admin/session', async (route) => {
    expect(route.request().headers().authorization).toBe(auth);
    await route.fulfill({ status: 200, json: { username: 'moderator', role: 'moderator' } });
  });
  await page.route('**/v1/admin/reports?*', async (route) => {
    expect(route.request().headers().authorization).toBe(auth);
    const requestedStatus = new URL(route.request().url()).searchParams.get('status');
    if (requestedStatus && requestedStatus !== 'all' && report.status !== requestedStatus) {
      await route.fulfill({ status: 200, json: { reports: [], limit: 100, offset: 0 } });
      return;
    }
    await route.fulfill({ status: 200, json: { reports: [report], limit: 100, offset: 0 } });
  });
  await page.route('**/v1/admin/reports/summary', async (route) => {
    expect(route.request().headers().authorization).toBe(auth);
    await route.fulfill({ status: 200, json: { open: report.status === 'open' ? 1 : 0, actioned: report.status === 'actioned' ? 1 : 0, dismissed: report.status === 'dismissed' ? 1 : 0 } });
  });
  await page.route('**/v1/admin/reports/d1a36e79-b31a-4c11-a9ae-b07181ff22ef', async (route) => {
    expect(route.request().headers().authorization).toBe(auth);
    reviewedPayload = route.request().postDataJSON();
    report.status = reviewedPayload.status;
    report.resolution_notes = reviewedPayload.notes;
    await route.fulfill({ status: 200, json: { report_id: report.report_id, status: report.status, reviewed_at: '2026-11-11T10:00:00Z' } });
  });

  await page.goto('/admin/');
  await page.getByLabel('Username').fill('moderator');
  await page.getByLabel('Password').fill('correct-horse-battery');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'Reports' })).toBeVisible();
  await expect(page.getByRole('row', { name: /#d1a36e79/ })).toBeVisible();
  await page.getByText('#d1a36e79').click();
  await expect(page.getByText('You should stop sending me this.')).toBeVisible();
  await page.getByLabel('Resolution notes').fill('Reviewed and escalated to the safety team.');
  await page.getByRole('button', { name: 'Mark actioned' }).click();
  expect(reviewedPayload).toEqual({ status: 'actioned', notes: 'Reviewed and escalated to the safety team.' });
  await page.getByRole('combobox').selectOption('actioned');
  await expect(page.getByText('#d1a36e79')).toBeVisible();
  await expect(page.getByText('actioned', { exact: true }).first()).toBeVisible();
});
