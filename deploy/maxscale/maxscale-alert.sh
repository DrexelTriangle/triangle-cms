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
# Colours are Discord's own palette, as decimal, so the embed's left bar reads
# at a glance without anyone parsing the text: red = act now, yellow = degraded,
# green = over, blurple = informational.
RED=15548997      # ED4245
YELLOW=16705372   # FEE75C
GREEN=5763719     # 57F287
BLURPLE=5793266   # 5865F2

case "$EVENT" in
  new_master)
    TITLE="Failover — replica promoted to primary"; COLOR=$RED
    NOTE="Writes are going to the new primary. Check the old one before it rejoins." ;;
  master_down|lost_master)
    TITLE="Primary database is down"; COLOR=$RED
    NOTE="Writes are failing until a promotion completes." ;;
  slave_down|lost_slave)
    TITLE="Replica is down"; COLOR=$YELLOW
    NOTE="Semi-sync dropped to async — writes are no longer guaranteed on two nodes." ;;
  master_up|slave_up|new_slave|server_up)
    # slave_up ([Down]->[Slave,Running]) is the recovery counterpart of
    # slave_down; new_slave ([Running]->[Slave,Running]) is a standalone node
    # joining. Both belong here or an outage never reports that it ended.
    TITLE="Database tier back to normal"; COLOR=$GREEN
    NOTE="Replication is healthy again." ;;
  server_down)
    TITLE="Database backend is down"; COLOR=$YELLOW
    NOTE="A backend stopped answering the monitor." ;;
  *)
    TITLE="MaxScale event"; COLOR=$BLURPLE
    NOTE="" ;;
esac

# MaxScale renders addresses as [10.248.40.155]:3306. The brackets are its
# IPv6-safe formatting and carry no meaning for an IPv4 pair, so strip them —
# they are pure noise in a notification someone reads on a phone.
INITIATOR_D=$(printf '%s' "$INITIATOR" | tr -d '[]')
NODELIST_D=$(printf '%s' "${NODELIST:-}" | tr -d '[]' | sed 's/,/, /g')

# --- Post to Discord, if configured ------------------------------------------
[ -r /etc/maxscale.secrets.d/alert.env ] && . /etc/maxscale.secrets.d/alert.env
[ -n "${DISCORD_WEBHOOK_URL:-}" ] || exit 0

# JSON string escaping: backslash and quote, then fold real newlines into \n.
# Literal newlines inside a JSON string are invalid and Discord rejects the
# payload outright. The ':a;N;$!ba' idiom slurps the whole input first.
json_esc() {
  printf '%s' "$1" \
    | sed 's/\\/\\\\/g; s/"/\\"/g' \
    | sed ':a;N;$!ba;s/\n/\\n/g'
}

# An embed rather than a plain message. Discord renders it with a coloured bar,
# a real title and aligned fields, which is both easier to triage at a glance
# and unmistakably a machine notice rather than someone talking. The `parent`
# value is dropped from the display -- it is usually "n/a" and never actionable
# -- but it is still written to the log above, where it costs nothing.
PAYLOAD=$(cat <<JSON
{"embeds":[{
  "title": "$(json_esc "$TITLE")",
  "description": "$(json_esc "$NOTE")",
  "color": ${COLOR},
  "fields": [
    {"name": "Node", "value": "$(json_esc "$INITIATOR_D")", "inline": true},
    {"name": "Event", "value": "\`$(json_esc "$EVENT")\`", "inline": true},
    {"name": "Cluster", "value": "$(json_esc "${NODELIST_D:-unknown}")", "inline": false}
  ],
  "footer": {"text": "MaxScale · $(json_esc "$(hostname)")"},
  "timestamp": "${TS}"
}]}
JSON
)

# --max-time well under script_timeout so a slow Discord cannot stall the
# monitor. Output is discarded: an error response body can echo the URL back.
# --fail makes curl return non-zero on a 4xx/5xx, which it does NOT do by
# default — without it a rejected payload or a revoked webhook looks like
# success and the failure is never logged.
curl -sS --fail -X POST \
  --max-time 10 \
  -H 'Content-Type: application/json' \
  --data "$PAYLOAD" \
  "$DISCORD_WEBHOOK_URL" >/dev/null 2>&1 \
  || printf '%s event=%s discord_post_failed\n' "$TS" "$EVENT" >> "$LOG" 2>/dev/null

exit 0
