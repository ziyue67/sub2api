export default {
    emailBroadcast: {
        title: 'Announcement Email Broadcast',
        description: 'Compose and bulk-send an announcement email to selected users or all registered users.',
        openComposer: 'Compose announcement email',
        form: {
            subject: 'Subject', subjectPlaceholder: 'e.g. Scheduled maintenance this Sunday', body: 'Body',
            bodyFormat: 'Format', bodyFormatText: 'Plain text',
            bodyPlaceholderHtml: 'Basic HTML is allowed; the server sanitizes it before sending.',
            bodyPlaceholderText: 'Plain text; line breaks are preserved.',
            bodyHint: 'HTML is sanitized for XSS; plain-text line breaks are converted automatically.',
            recipients: 'Recipients', sendToAll: 'Send to all registered users',
            sendToAllHint: 'When enabled, manually picked recipients are ignored.',
            searchRecipientsPlaceholder: 'Search by email or username…', noRecipientsFound: 'No matching users',
            noRecipientsSelected: 'Pick at least one recipient, or enable sending to all registered users.',
            selectedRecipient: 'Selected recipient', singleRecipientHint: 'This email will be sent only to this user.',
            send: 'Send broadcast', sending: 'Sending…'
        },
        recipientsMode: { all: 'All users', selected: 'Selected recipients' },
        status: { pending: 'Queued', sending: 'Sending', completed: 'Completed', failed: 'Failed' },
        preview: {
            title: 'Live preview', refreshing: 'Refreshing…', error: 'Failed to render preview',
            hint: 'Preview matches the final delivered email.', iframeTitle: 'Broadcast email preview',
            placeholderSubject: '(Fill in subject on the left)',
            placeholderBody: '(Compose the body on the left; the preview updates as you type.)'
        },
        toolbar: {
            p: 'Paragraph', b: 'Bold', i: 'Italic', a: 'Insert link', ul: 'Bullet list', h2: 'Heading 2', hr: 'Horizontal rule', br: 'Line break'
        },
        history: {
            title: 'Recent broadcasts (last 10)', empty: 'No broadcasts sent yet', preview: 'Preview', delete: 'Delete',
            backToList: 'Back to history', deleteConfirmTitle: 'Delete broadcast',
            deleteConfirm: 'Delete the broadcast "{subject}"?',
            detail: { subject: 'Subject', status: 'Status', recipients: 'Recipients', sentAt: 'Sent at', errorMessage: 'Error', iframeTitle: 'Historical broadcast preview' }
        },
        notifications: { sendQueued: 'Broadcast #{id} queued for delivery.', loadHistoryFailed: 'Failed to load broadcast history', deleteSuccess: 'Broadcast deleted' }
    }
};
