package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"server/internal/activity"
	"server/internal/models"
	"server/internal/scraper"
)

// A post or a reel in canonical form. The editor normalises what the writer
// pasted to this shape before calling, and the sidecar loads /embed/ under it.
var instagramPostURL = regexp.MustCompile(`^https://www\.instagram\.com/(p|reel)/[A-Za-z0-9_-]+/$`)

// maxInstagramImages caps a carousel. Instagram allows 20; ten is plenty for
// an article and bounds what one request can write to the media tree.
const maxInstagramImages = 10

// downloadRemote copies one public URL to a temp file through the same guarded
// client as a paste sideload: no private addresses, size capped, the reason
// for a failure logged rather than echoed. A variable so the handler tests can
// stand in for the network. The caller closes and removes the file.
var downloadRemote = func(ctx context.Context, target *url.URL) (*os.File, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "image/*")
	resp, err := remoteImageClient(remoteFetchTimeout).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("remote server returned %d", resp.StatusCode)
	}
	maxBytes := maxUploadBytes()
	tmp, err := os.CreateTemp("", "instagram-*")
	if err != nil {
		return nil, err
	}
	written, err := io.Copy(tmp, io.LimitReader(resp.Body, maxBytes+1))
	if err == nil && written > maxBytes {
		err = fmt.Errorf("file exceeds maximum size of %d bytes", maxBytes)
	}
	if err == nil {
		_, err = tmp.Seek(0, io.SeekStart)
	}
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, err
	}
	return tmp, nil
}

// PostInstagramEmbed copies an Instagram post's images into the media library
// so Scalene can show the post in its own holder. The sidecar reads the image
// links out of the post; this handler stores the bytes and returns the
// wp-content paths the editor writes into the shortcode's media attribute.
//
// @Summary Import an Instagram post's images into the media library
// @Tags embeds
// @Accept json
// @Produce json
// @Param body body models.InstagramEmbedRequest true "Post URL"
// @Success 201 {object} models.InstagramEmbedResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 422 {object} models.ErrorResponse
// @Failure 502 {object} models.ErrorResponse
// @Failure 503 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/embeds/instagram [post]
func PostInstagramEmbed(conn *sql.DB, scrape *scraper.Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := CheckMediaStorage(); err != nil {
			writeStoreImageError(w, err)
			return
		}
		var body models.InstagramEmbedRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		post := strings.TrimSpace(body.URL)
		if !instagramPostURL.MatchString(post) {
			writeError(w, http.StatusBadRequest, "url must be an Instagram post or reel link")
			return
		}
		if !scrape.Enabled() {
			writeError(w, http.StatusServiceUnavailable, "Instagram image import is not set up on this server")
			return
		}

		images, err := scrape.InstagramImages(r.Context(), post)
		if err != nil {
			slog.Warn("instagram import: sidecar failed", "post", post, "error", err)
			writeError(w, http.StatusBadGateway, "could not read the images from that post")
			return
		}
		if len(images) == 0 {
			writeError(w, http.StatusUnprocessableEntity, "no images were found on that post")
			return
		}
		if len(images) > maxInstagramImages {
			images = images[:maxInstagramImages]
		}

		media := make([]string, 0, len(images))
		for _, img := range images {
			target, err := url.Parse(img)
			if err != nil || target.Scheme != "https" || target.Host == "" {
				writeError(w, http.StatusBadGateway, "the post returned an unusable image link")
				return
			}
			tmp, err := downloadRemote(r.Context(), target)
			if err != nil {
				slog.Warn("instagram import: image fetch failed", "post", post, "error", err)
				writeError(w, http.StatusBadGateway, "could not copy an image from that post")
				return
			}
			stored, err := storeImage(r.Context(), conn, tmp, remoteFileName(target))
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
			if err != nil {
				writeStoreImageError(w, err)
				return
			}
			media = append(media, "/"+strings.TrimLeft(stored.Path, "/"))
		}

		activity.LogRequest(r, "instagram_imported", path.Base(post), "post", post, "images", strconv.Itoa(len(media)))
		writeJSON(w, http.StatusCreated, models.InstagramEmbedResponse{Media: media})
	})
}
