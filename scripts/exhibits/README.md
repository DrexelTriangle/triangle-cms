# Exhibit of the day drafts

`exhibits.json` is the sourced inventory of Academy of Natural Sciences exhibits and specimens (ansp.org, Wikimedia Commons, and the sources listed per entry). Entries with `"verified": false` have no confirmed opening date, so the script leaves tenure out of their copy.

`python3 ../generate_exhibit_spotlights.py` writes `exhibit_spotlights.draft.json` with every entry disabled. Use `--only <slug>` for one entry. The script calls the `claude` CLI, or the Messages API when `ANTHROPIC_API_KEY` is set.

Editorial steps: read each body against its sources and fix or cut anything unsupported. Convert the chosen Commons image to WebP, place it under `wp-content/uploads/scalene/exhibits/` with a filename that has never been used, and put its URL in `image_url`. Set `feature_date`, flip `enabled`, then send the array as `{"entries": [...]}` to `PATCH /v1/settings/exhibit-spotlights` with an admin token. The request replaces the whole list, so include every entry you want kept. `GET` on the same route returns the current list and also needs an admin token, since it includes unpublished drafts. Image and credit URLs must be https (or site-relative) or the request is rejected.
