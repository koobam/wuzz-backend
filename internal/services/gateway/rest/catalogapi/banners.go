package catalogapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	catalogv1 "github.com/koobam/wuzz-backend/gen/wuzz/catalog/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Banners of the customer app: the back office writes them, the app reads the ones that are live. Whether a banner is live and where a
// tap leads (the deeplink) are worked out by the catalog service from its own clock and rules; this file only carries them.

type bannerOutletDTO struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// BannerDTO is a banner for the back office. starts_at and ends_at are RFC 3339 in UTC, or null when the schedule is open there;
// outlet_ids is empty for a banner shown at every outlet.
type BannerDTO struct {
	ID            int64             `json:"id"`
	Title         string            `json:"title"`
	InternalNotes string            `json:"internal_notes"`
	ImageURL      string            `json:"image_url"`
	TargetType    string            `json:"target_type"`
	TargetValue   string            `json:"target_value"`
	Deeplink      string            `json:"deeplink"`
	Status        string            `json:"status"`
	SortIndex     int32             `json:"sort_index"`
	StartsAt      *string           `json:"starts_at"`
	EndsAt        *string           `json:"ends_at"`
	OutletIDs     []int64           `json:"outlet_ids"`
	Outlets       []bannerOutletDTO `json:"outlets"`
	IsLive        bool              `json:"is_live"`
	CreatedAt     string            `json:"created_at"`
	UpdatedAt     string            `json:"updated_at"`
}

func optUTC(t *timestamppb.Timestamp) *string {
	if t == nil {
		return nil
	}
	s := utc(t)
	return &s
}

func bannerDTO(b *catalogv1.Banner) BannerDTO {
	d := BannerDTO{ID: b.GetId(), Title: b.GetTitle(), InternalNotes: b.GetInternalNotes(), ImageURL: b.GetImageUrl(), TargetType: b.GetTargetType(),
		TargetValue: b.GetTargetValue(), Deeplink: b.GetDeeplink(), Status: b.GetStatus(), SortIndex: b.GetSortIndex(), StartsAt: optUTC(b.GetStartsAt()),
		EndsAt: optUTC(b.GetEndsAt()), OutletIDs: append([]int64{}, b.GetOutletIds()...), Outlets: make([]bannerOutletDTO, 0, len(b.GetOutlets())),
		IsLive: b.GetIsLive(), CreatedAt: utc(b.GetCreatedAt()), UpdatedAt: utc(b.GetUpdatedAt())}
	for _, o := range b.GetOutlets() {
		d.Outlets = append(d.Outlets, bannerOutletDTO{ID: o.GetId(), Name: o.GetName()})
	}
	return d
}

func bannerDTOs(in []*catalogv1.Banner) []BannerDTO {
	out := make([]BannerDTO, 0, len(in))
	for _, b := range in {
		out = append(out, bannerDTO(b))
	}
	return out
}

// flexString reads a JSON string or number as text, so a service id may be sent as 12 or "12".
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case string(b) == "null":
		*f = ""
	case len(b) > 0 && b[0] == '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexString(s)
	default:
		var n json.Number
		if err := json.Unmarshal(b, &n); err != nil {
			return apperr.Invalid("target_value must be text or a number")
		}
		*f = flexString(n.String())
	}
	return nil
}

type bannerBody struct {
	Title         string     `json:"title"`
	InternalNotes string     `json:"internal_notes"`
	ImageURL      string     `json:"image_url"`
	TargetType    string     `json:"target_type"`
	TargetValue   flexString `json:"target_value"`
	Status        string     `json:"status"`
	StartsAt      *string    `json:"starts_at"` // RFC 3339; null or "" = open-ended
	EndsAt        *string    `json:"ends_at"`
	OutletIDs     []int64    `json:"outlet_ids"`
}

func rfc3339(name string, s *string) (*timestamppb.Timestamp, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return nil, apperr.Invalid(name + " must be a date and time such as 2026-10-28T09:00:00+07:00")
	}
	return timestamppb.New(t), nil
}

func (b bannerBody) proto() (*catalogv1.Banner, error) {
	starts, err := rfc3339("starts_at", b.StartsAt)
	if err != nil {
		return nil, err
	}
	ends, err := rfc3339("ends_at", b.EndsAt)
	if err != nil {
		return nil, err
	}
	return &catalogv1.Banner{Title: b.Title, InternalNotes: b.InternalNotes, ImageUrl: b.ImageURL, TargetType: b.TargetType, TargetValue: string(b.TargetValue),
		Status: b.Status, StartsAt: starts, EndsAt: ends, OutletIds: b.OutletIDs}, nil
}

func bannerID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(c, apperr.Invalid("The banner id is invalid"))
		return 0, false
	}
	return id, true
}

// BOListBanners answers GET /backoffice/v1/banners: q, status (active|inactive), live (true|false), outlet_id (banners that apply to
// the outlet), sort_by (id, title, status, sort_index, starts_at, ends_at, created_at, updated_at; default sort_index), sort_dir, limit, offset.
func (h *Handler) BOListBanners(c *gin.Context) {
	outlet := int64(0)
	if v := c.Query("outlet_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			httpx.Fail(c, apperr.Invalid("outlet_id must be a positive number"))
			return
		}
		outlet = n
	}
	res, err := h.Catalog.ListBanners(c.Request.Context(), &catalogv1.ListBannersRequest{
		Q: c.Query("q"), Status: c.Query("status"), Live: c.Query("live"), OutletId: outlet, SortBy: c.Query("sort_by"), SortDir: c.Query("sort_dir"),
		Limit: int32(httpx.IntQuery(c, "limit", 10)), Offset: int32(httpx.IntQuery(c, "offset", 0))})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, gin.H{"items": bannerDTOs(res.GetBanners()), "total": res.GetTotal()})
}

func (h *Handler) BOGetBanner(c *gin.Context) {
	id, ok := bannerID(c)
	if !ok {
		return
	}
	res, err := h.Catalog.GetBanner(c.Request.Context(), &catalogv1.GetBannerRequest{Id: id})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, bannerDTO(res.GetBanner()))
}

func (h *Handler) BOCreateBanner(c *gin.Context) {
	body, ok := bindJSON[bannerBody](c)
	if !ok {
		return
	}
	b, err := body.proto()
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	res, err := h.Catalog.CreateBanner(c.Request.Context(), &catalogv1.SaveBannerRequest{Banner: b})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.Created(c, bannerDTO(res.GetBanner()))
}

// BOUpdateBanner replaces everything about the banner but its place in the carousel (that is BOReorderBanners).
func (h *Handler) BOUpdateBanner(c *gin.Context) {
	id, ok := bannerID(c)
	if !ok {
		return
	}
	body, ok := bindJSON[bannerBody](c)
	if !ok {
		return
	}
	b, err := body.proto()
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	res, err := h.Catalog.UpdateBanner(c.Request.Context(), &catalogv1.SaveBannerRequest{Id: id, Banner: b})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, bannerDTO(res.GetBanner()))
}

// BOSetBannerStatus switches a banner on or off: PATCH {"status": "active" | "inactive"}.
func (h *Handler) BOSetBannerStatus(c *gin.Context) {
	id, ok := bannerID(c)
	if !ok {
		return
	}
	body, ok := bindJSON[struct {
		Status string `json:"status"`
	}](c)
	if !ok {
		return
	}
	res, err := h.Catalog.SetBannerStatus(c.Request.Context(), &catalogv1.SetBannerStatusRequest{Id: id, Status: body.Status})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, bannerDTO(res.GetBanner()))
}

func (h *Handler) BODeleteBanner(c *gin.Context) {
	id, ok := bannerID(c)
	if !ok {
		return
	}
	// The title is read first only so the activity log can say which banner went; an unknown id is a 404 here already.
	got, err := h.Catalog.GetBanner(c.Request.Context(), &catalogv1.GetBannerRequest{Id: id})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	if _, err := h.Catalog.DeleteBanner(c.Request.Context(), &catalogv1.GetBannerRequest{Id: id}); err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, gin.H{"id": id, "title": got.GetBanner().GetTitle()})
}

// BOReorderBanners writes the carousel's order: {"ids": [..]} must list every banner exactly once, first shown first. It answers with
// the list in the new order.
func (h *Handler) BOReorderBanners(c *gin.Context) {
	body, ok := bindJSON[struct {
		IDs []int64 `json:"ids"`
	}](c)
	if !ok {
		return
	}
	res, err := h.Catalog.ReorderBanners(c.Request.Context(), &catalogv1.ReorderBannersRequest{Ids: body.IDs})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, gin.H{"items": bannerDTOs(res.GetBanners()), "total": res.GetTotal()})
}

// BOListBannerScreens are the screens of the app a banner may open, by key.
func (h *Handler) BOListBannerScreens(c *gin.Context) {
	res, err := h.Catalog.ListBannerScreens(c.Request.Context(), &catalogv1.ListBannerScreensRequest{})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, gin.H{"screens": res.GetScreens()})
}

// LiveBanners answers GET /app/v1/banners?outlet_id=: the banners on a phone right now, first shown first. With an outlet, those for
// every outlet and those limited to it; without, only those for every outlet. Nothing private is in it: no note, no status, no limit.
func (h *Handler) LiveBanners(c *gin.Context) {
	outlet := int64(0)
	if v := c.Query("outlet_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			httpx.Fail(c, apperr.Invalid(fmt.Sprintf("outlet_id must be a positive number, not %q", v)))
			return
		}
		outlet = n
	}
	res, err := h.Catalog.ListLiveBanners(c.Request.Context(), &catalogv1.ListLiveBannersRequest{OutletId: outlet})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	type liveDTO struct {
		ID          int64  `json:"id"`
		Title       string `json:"title"`
		ImageURL    string `json:"image_url"`
		TargetType  string `json:"target_type"`
		TargetValue string `json:"target_value"`
		Deeplink    string `json:"deeplink"`
	}
	out := make([]liveDTO, 0, len(res.GetBanners()))
	for _, b := range res.GetBanners() {
		out = append(out, liveDTO{ID: b.GetId(), Title: b.GetTitle(), ImageURL: b.GetImageUrl(), TargetType: b.GetTargetType(), TargetValue: b.GetTargetValue(), Deeplink: b.GetDeeplink()})
	}
	httpx.OK(c, gin.H{"banners": out})
}
