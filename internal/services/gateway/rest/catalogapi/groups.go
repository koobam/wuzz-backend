package catalogapi

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "github.com/koobam/wuzz-backend/gen/wuzz/catalog/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
)

// Variant and modifier groups: reusable sets of options that services link to. A variant group is a choice of exactly one
// option, each adding its price_delta to the service's price; a modifier group is optional add-ons under a min/max rule.

type serviceRefDTO struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type variantOptionDTO struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	PriceDelta        int64  `json:"price_delta"`
	IsDefault         bool   `json:"is_default"`
	Active            bool   `json:"active"`
	SortIndex         int32  `json:"sort_index"`
	EstimatedTime     int32  `json:"estimated_time"`
	EstimatedTimeUnit string `json:"estimated_time_unit"`
}

// VariantGroupDTO is a variant group as the back office and the POS read it.
type VariantGroupDTO struct {
	ID            int64              `json:"id"`
	Name          string             `json:"name"`
	InternalNotes string             `json:"internal_notes"`
	SortIndex     int32              `json:"sort_index"`
	Options       []variantOptionDTO `json:"options"`
	Services      []serviceRefDTO    `json:"services"`
	// RFC 3339, UTC.
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type modifierOptionDTO struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Price       int64  `json:"price"`
	Active      bool   `json:"active"`
	SortIndex   int32  `json:"sort_index"`
}

// ModifierGroupDTO is a modifier group; Max is null when there is no ceiling.
type ModifierGroupDTO struct {
	ID            int64               `json:"id"`
	Name          string              `json:"name"`
	InternalNotes string              `json:"internal_notes"`
	Min           int32               `json:"min"`
	Max           *int32              `json:"max"`
	Options       []modifierOptionDTO `json:"options"`
	Services      []serviceRefDTO     `json:"services"`
	CreatedAt     string              `json:"created_at"`
	UpdatedAt     string              `json:"updated_at"`
}

// utc is a timestamp as RFC 3339 in UTC, or "" when there is none.
func utc(t *timestamppb.Timestamp) string {
	if t == nil {
		return ""
	}
	return t.AsTime().UTC().Format(time.RFC3339)
}

func refDTOs(in []*catalogv1.ServiceRef) []serviceRefDTO {
	out := make([]serviceRefDTO, 0, len(in))
	for _, s := range in {
		out = append(out, serviceRefDTO{ID: s.GetId(), Name: s.GetName()})
	}
	return out
}

func variantGroupDTO(g *catalogv1.VariantGroup) VariantGroupDTO {
	out := VariantGroupDTO{ID: g.GetId(), Name: g.GetName(), InternalNotes: g.GetInternalNotes(), SortIndex: g.GetSortIndex(),
		Options: make([]variantOptionDTO, 0, len(g.GetOptions())), Services: refDTOs(g.GetServices()),
		CreatedAt: utc(g.GetCreatedAt()), UpdatedAt: utc(g.GetUpdatedAt())}
	for _, o := range g.GetOptions() {
		out.Options = append(out.Options, variantOptionDTO{
			ID: o.GetId(), Name: o.GetName(), PriceDelta: o.GetPriceDelta(), IsDefault: o.GetIsDefault(), Active: o.GetActive(),
			SortIndex: o.GetSortIndex(), EstimatedTime: o.GetEstimatedTime(), EstimatedTimeUnit: o.GetEstimatedTimeUnit(),
		})
	}
	return out
}

func variantGroupDTOs(in []*catalogv1.VariantGroup) []VariantGroupDTO {
	out := make([]VariantGroupDTO, 0, len(in))
	for _, g := range in {
		out = append(out, variantGroupDTO(g))
	}
	return out
}

func modifierGroupDTO(g *catalogv1.ModifierGroup) ModifierGroupDTO {
	out := ModifierGroupDTO{ID: g.GetId(), Name: g.GetName(), InternalNotes: g.GetInternalNotes(), Min: g.GetMin(),
		Options: make([]modifierOptionDTO, 0, len(g.GetOptions())), Services: refDTOs(g.GetServices()),
		CreatedAt: utc(g.GetCreatedAt()), UpdatedAt: utc(g.GetUpdatedAt())}
	if g.Max != nil {
		m := g.GetMax()
		out.Max = &m
	}
	for _, o := range g.GetOptions() {
		out.Options = append(out.Options, modifierOptionDTO{
			ID: o.GetId(), Name: o.GetName(), Description: o.GetDescription(), Price: o.GetPrice(), Active: o.GetActive(), SortIndex: o.GetSortIndex(),
		})
	}
	return out
}

func modifierGroupDTOs(in []*catalogv1.ModifierGroup) []ModifierGroupDTO {
	out := make([]ModifierGroupDTO, 0, len(in))
	for _, g := range in {
		out = append(out, modifierGroupDTO(g))
	}
	return out
}

// withoutNotes drops the private back-office notes from the groups of a service, for the POS, which is staff but not head office.
func withoutNotes(s ServiceDTO) ServiceDTO {
	for i := range s.VariantGroups {
		s.VariantGroups[i].InternalNotes = ""
	}
	for i := range s.ModifierGroups {
		s.ModifierGroups[i].InternalNotes = ""
	}
	return s
}

type variantGroupBody struct {
	Name          string `json:"name"`
	InternalNotes string `json:"internal_notes"`
	Options       []struct {
		ID                int64  `json:"id"`
		Name              string `json:"name"`
		PriceDelta        int64  `json:"price_delta"`
		IsDefault         bool   `json:"is_default"`
		Active            *bool  `json:"active"` // left out = active
		EstimatedTime     int32  `json:"estimated_time"`
		EstimatedTimeUnit string `json:"estimated_time_unit"`
	} `json:"options"`
}

func (b variantGroupBody) proto() *catalogv1.VariantGroup {
	g := &catalogv1.VariantGroup{Name: b.Name, InternalNotes: b.InternalNotes}
	for _, o := range b.Options {
		g.Options = append(g.Options, &catalogv1.VariantOption{
			Id: o.ID, Name: o.Name, PriceDelta: o.PriceDelta, IsDefault: o.IsDefault, Active: o.Active == nil || *o.Active,
			EstimatedTime: o.EstimatedTime, EstimatedTimeUnit: o.EstimatedTimeUnit,
		})
	}
	return g
}

type modifierGroupBody struct {
	Name          string `json:"name"`
	InternalNotes string `json:"internal_notes"`
	Min           int32  `json:"min"`
	Max           *int32 `json:"max"` // null or left out = no ceiling
	Options       []struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Price       int64  `json:"price"`
		Active      *bool  `json:"active"` // left out = active
	} `json:"options"`
}

func (b modifierGroupBody) proto() *catalogv1.ModifierGroup {
	g := &catalogv1.ModifierGroup{Name: b.Name, InternalNotes: b.InternalNotes, Min: b.Min, Max: b.Max}
	for _, o := range b.Options {
		g.Options = append(g.Options, &catalogv1.ModifierOption{
			Id: o.ID, Name: o.Name, Description: o.Description, Price: o.Price, Active: o.Active == nil || *o.Active,
		})
	}
	return g
}

func idListProto(in *[]int64) *catalogv1.IdList {
	if in == nil {
		return nil
	}
	return &catalogv1.IdList{Ids: *in}
}

func groupID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(c, apperr.Invalid("The group id is invalid"))
		return 0, false
	}
	return id, true
}

// ---- variant groups ----

func (h *Handler) BOListVariantGroups(c *gin.Context) {
	res, err := h.Catalog.ListVariantGroups(c.Request.Context(), &catalogv1.ListVariantGroupsRequest{
		Q: c.Query("q"), SortBy: c.Query("sort_by"), SortDir: c.Query("sort_dir"),
		Limit: int32(httpx.IntQuery(c, "limit", 10)), Offset: int32(httpx.IntQuery(c, "offset", 0)),
		ServiceId: int64(httpx.IntQuery(c, "service_id", 0)), Usage: c.Query("usage")})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, gin.H{"items": variantGroupDTOs(res.GetGroups()), "total": res.GetTotal()})
}

func (h *Handler) BOGetVariantGroup(c *gin.Context) {
	id, ok := groupID(c)
	if !ok {
		return
	}
	res, err := h.Catalog.GetVariantGroup(c.Request.Context(), &catalogv1.GetVariantGroupRequest{Id: id})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, variantGroupDTO(res.GetGroup()))
}

func (h *Handler) BOCreateVariantGroup(c *gin.Context) {
	b, ok := httpx.Bind[variantGroupBody](c)
	if !ok {
		return
	}
	res, err := h.Catalog.CreateVariantGroup(c.Request.Context(), &catalogv1.SaveVariantGroupRequest{Group: b.proto()})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.Created(c, variantGroupDTO(res.GetGroup()))
}

func (h *Handler) BOUpdateVariantGroup(c *gin.Context) {
	id, ok := groupID(c)
	if !ok {
		return
	}
	b, ok := httpx.Bind[variantGroupBody](c)
	if !ok {
		return
	}
	res, err := h.Catalog.UpdateVariantGroup(c.Request.Context(), &catalogv1.SaveVariantGroupRequest{Id: id, Group: b.proto()})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, variantGroupDTO(res.GetGroup()))
}

func (h *Handler) BODeleteVariantGroup(c *gin.Context) {
	id, ok := groupID(c)
	if !ok {
		return
	}
	if _, err := h.Catalog.DeleteVariantGroup(c.Request.Context(), &catalogv1.DeleteGroupRequest{Id: id}); err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, gin.H{"id": id})
}

// ---- modifier groups ----

func (h *Handler) BOListModifierGroups(c *gin.Context) {
	res, err := h.Catalog.ListModifierGroups(c.Request.Context(), &catalogv1.ListModifierGroupsRequest{
		Q: c.Query("q"), SortBy: c.Query("sort_by"), SortDir: c.Query("sort_dir"), Requirement: c.Query("requirement"),
		Limit: int32(httpx.IntQuery(c, "limit", 10)), Offset: int32(httpx.IntQuery(c, "offset", 0)),
		ServiceId: int64(httpx.IntQuery(c, "service_id", 0)), Usage: c.Query("usage")})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, gin.H{"items": modifierGroupDTOs(res.GetGroups()), "total": res.GetTotal()})
}

func (h *Handler) BOGetModifierGroup(c *gin.Context) {
	id, ok := groupID(c)
	if !ok {
		return
	}
	res, err := h.Catalog.GetModifierGroup(c.Request.Context(), &catalogv1.GetModifierGroupRequest{Id: id})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, modifierGroupDTO(res.GetGroup()))
}

func (h *Handler) BOCreateModifierGroup(c *gin.Context) {
	b, ok := httpx.Bind[modifierGroupBody](c)
	if !ok {
		return
	}
	res, err := h.Catalog.CreateModifierGroup(c.Request.Context(), &catalogv1.SaveModifierGroupRequest{Group: b.proto()})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.Created(c, modifierGroupDTO(res.GetGroup()))
}

func (h *Handler) BOUpdateModifierGroup(c *gin.Context) {
	id, ok := groupID(c)
	if !ok {
		return
	}
	b, ok := httpx.Bind[modifierGroupBody](c)
	if !ok {
		return
	}
	res, err := h.Catalog.UpdateModifierGroup(c.Request.Context(), &catalogv1.SaveModifierGroupRequest{Id: id, Group: b.proto()})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, modifierGroupDTO(res.GetGroup()))
}

func (h *Handler) BODeleteModifierGroup(c *gin.Context) {
	id, ok := groupID(c)
	if !ok {
		return
	}
	if _, err := h.Catalog.DeleteModifierGroup(c.Request.Context(), &catalogv1.DeleteGroupRequest{Id: id}); err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, gin.H{"id": id})
}
