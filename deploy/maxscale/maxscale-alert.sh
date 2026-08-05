#!/bin/sh
# Alert hook for MaxScale's mariadbmon, invoked via `script=` in maxscale.cnf.
# Installed on THETRIANGLE-MAXSCALE as /usr/local/bin/maxscale-alert.sh (0755).
#
# MaxScale runs this as the `maxscale` user on every event listed in `events=`,
# substituting $EVENT/$INITIATOR/$NODELIST/$PARENT before exec. It is bounded by
# script_timeout (90s) — if it hangs, the monitor blocks, so every outbound call
# here MUST have its own timeout.
#
# Design notes:
#   - It ALWAYS writes the local log first and posts to Discord second. A failover
#     that happened is a fact worth keeping even if Discord is unreachable, and
#     the log is what you correlate against maxscale.log afterwards.
#   - It exits 0 unconditionally. A non-zero exit here is noise in maxscale.log
#     and there is nothing MaxScale can usefully do about a failed notification.
#   - Until DISCORD_WEBHOOK_URL is configured it degrades to log-only rather than
#     failing, so it is safe to install before the webhook exists.
#
# The webhook lives in /etc/maxscale.secrets.d/alert.env (0640 root:maxscale),
# NOT here and NOT in maxscale.cnf, which is world-readable 0644:
#   DISCORD_WEBHOOK_URL=https://discord.com/api/webhooks/...
#
# Discord's NATIVE payload is used ({"content": ...}), not Slack's
# ({"text": ...}). Discord will accept Slack-shaped payloads if you append
# /slack to the webhook URL, but that path silently drops formatting it does not
# understand, so the native field is the honest choice. Note the markup differs
# from Slack: bold is **text**, not *text*.
set -u

EVENT="${1:-unknown}"
INITIATOR="${2:-unknown}"
NODELIST="${3:-}"
PARENT="${4:-}"

TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LOG=/var/log/maxscale/failover-events.log

# --- Always record locally, before anything that can fail --------------------
printf '%s event=%s initiator=%s nodes=%s parent=%s\n' \
  "$TS" "$EVENT" "$INITIATOR" "$NODELIST" "$PARENT" >> "$LOG" 2>/dev/null

# --- Classify -----------------------------------------------------------------
# new_master is the one that means an automated failover actually promoted a
# node. lost_slave/slave_down matter more than they look: with only two nodes,
# losing the replica means semi-sync degrades to async
# (rpl_semi_sync_master_wait_no_slave=OFF) and the zero-data-loss guarantee
# stops holding until it returns.
case "$EVENT" in
  new_master)
    ICON=":rotating_light:"; SEV="FAILOVER"
    NOTE="A node was promoted. The old primary needs checking before it is trusted again." ;;
  master_down|lost_master)
    ICON=":rotating_light:"; SEV="PRIMARY DOWN"
    NOTE="Writes are failing until a promotion completes." ;;
  slave_down|lost_slave)
    ICON=":warning:"; SEV="REPLICA DOWN"
    NOTE="Semi-sync has degraded to async: writes are no longer guaranteed durable on two nodes." ;;
  master_up|slave_up|new_slave|server_up)
    # slave_up ([Down]->[Slave,Running]) is the recovery counterpart of
    # slave_down; new_slave ([Running]->[Slave,Running]) is a standalone node
    # joining. Both belong here or an outage never reports that it ended.
    ICON=":white_check_mark:"; SEV="RECOVERED"
    NOTE="Confirm with 'maxctrl list servers' and check Rpl_semi_sync_master_clients=1 on the primary." ;;
  server_down)
    ICON=":warning:"; SEV="SERVER DOWN"
    NOTE="A backend stopped responding to the monitor." ;;
  *)
    ICON=":information_source:"; SEV="EVENT"
    NOTE="" ;;
esac

# --- Post to Discord, if configured ------------------------------------------
[ -r /etc/maxscale.secrets.d/alert.env ] && . /etc/maxscale.secrets.d/alert.env
[ -n "${DISCORD_WEBHOOK_URL:-}" ] || exit 0

# Build the message with RAW values, then escape exactly once when it becomes
# JSON. Escaping the fields individually and then escaping the whole string
# again would double every backslash.
# Discord markdown: **bold**, `code`. Single asterisks would render as italics.
TEXT="${ICON} **MaxScale ${SEV}** — \`${EVENT}\`
**initiator:** ${INITIATOR}
**nodes:** ${NODELIST:-n/a}
**parent:** ${PARENT:-n/a}
**time:** ${TS}
${NOTE}"

# JSON string escaping: backslash and quote, then fold the real newlines into
# \n. Literal newlines inside a JSON string are invalid and Discord rejects the
# payload outright, so the multi-line message above must be collapsed here.
# The ':a;N;$!ba' idiom slurps the whole input before substituting.
json_esc() {
  printf '%s' "$1" \
    | sed 's/\\/\\\\/g; s/"/\\"/g' \
    | sed ':a;N;$!ba;s/\n/\\n/g'
}

# --max-time well under script_timeout so a slow Discord cannot stall the
# monitor. Output is discarded: an error response body can echo the URL back.
# --fail makes curl return non-zero on a 4xx/5xx, which it does NOT do by
# default — without it a rejected payload or a revoked webhook looks like
# success and the failure is never logged.
curl -sS --fail -X POST \
  --max-time 10 \
  -H 'Content-Type: application/json' \
  --data "{\"content\":\"$(json_esc "$TEXT")\"}" \
  "$DISCORD_WEBHOOK_URL" >/dev/null 2>&1 \
  || printf '%s event=%s discord_post_failed\n' "$TS" "$EVENT" >> "$LOG" 2>/dev/null

exit 0
