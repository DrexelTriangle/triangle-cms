// The newsletter API contract served by /v1/newsletters (see
// docs/newsletter-api.md). The mock store in mockStore.ts implements the same
// shapes in the browser for builds without VITE_NEWSLETTER_API=live.

export type NewsletterStatus = "draft" | "scheduled" | "sending" | "sent"

// The paid banner that sits above the masthead. Optional per issue.
export type NewsletterSponsor = {
  image_url: string
  link_url: string
  alt: string
}

export type Newsletter = {
  id: number
  subject: string
  // The inbox preview line shown after the subject. Hidden in the email body.
  preheader: string
  status: NewsletterStatus
  sponsor: NewsletterSponsor | null
  // Everything between the masthead and the footer, in the same article HTML
  // the article editor saves (Trix figures already reduced to wp-caption).
  body_html: string
  // The full rendered email, built by the dashboard from the fields above so
  // the server never needs its own copy of the template. Carries
  // {{unsubscribe_url}}, {{manage_subscription_url}} and {{view_online_url}}
  // for the sender to substitute per recipient.
  email_html: string
  scheduled_for: string | null
  sent_at: string | null
  recipient_count: number
  open_count: number
  created_at: string
  updated_at: string
}

export type NewsletterInput = Pick<Newsletter, "subject" | "preheader" | "sponsor" | "body_html" | "email_html">

export type NewsletterSubscriberStatus = "confirmed" | "unsubscribed" | "bounced"

export type NewsletterSubscriber = {
  id: number
  email: string
  // Often empty: the WordPress signup form only asked for an email.
  name: string
  status: NewsletterSubscriberStatus
  // The WordPress plugin's list ids (1 Drexel Students … 7 Other).
  lists: number[]
  subscribed_at: string
  unsubscribed_at?: string
}

export type NewsletterStats = {
  subscriber_count: number
  sent_count: number
  recipients_reached: number
  // 0..1 across sent issues with at least one recipient; null when none.
  avg_open_rate: number | null
}

export type NewsletterApi = {
  // "mock" until the backend lands; the UI uses it to disable sending.
  backend: "live" | "mock"
  list: () => Promise<Newsletter[]>
  get: (id: number) => Promise<Newsletter>
  create: (input: NewsletterInput) => Promise<Newsletter>
  update: (id: number, input: NewsletterInput) => Promise<Newsletter>
  remove: (id: number) => Promise<void>
  schedule: (id: number, sendAt: string) => Promise<Newsletter>
  unschedule: (id: number) => Promise<Newsletter>
  sendTest: (id: number, emails: string[]) => Promise<void>
  stats: () => Promise<NewsletterStats>
  listSubscribers: () => Promise<NewsletterSubscriber[]>
  removeSubscriber: (id: number) => Promise<void>
}
