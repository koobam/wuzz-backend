// Package app wires catalog's use cases: a write side in app/command and a read side in app/query, one handler per
// use case. Nothing here holds a rule of its own; the rules live in the handlers and in the domain.
//
// Handlers state the few repository methods they need as small ports (command.Outlets, query.Stock, ...). Store is
// their union: the whole of what the Postgres adapter has to provide, so a missing method fails to compile here.
package app

import (
	"github.com/koobam/wuzz-backend/internal/services/catalog/app/command"
	"github.com/koobam/wuzz-backend/internal/services/catalog/app/query"
)

// Store is every repository method the handlers use, implemented by adapters/postgres.
type Store interface {
	command.Outlets
	command.Services
	command.VariantGroups
	command.ModifierGroups
	command.Materials
	command.Recipes
	command.Banners
	command.Vouchers
	command.Stock

	query.Outlets
	query.Services
	query.VariantGroups
	query.ModifierGroups
	query.Materials
	query.Recipes
	query.Costing
	query.Banners
	query.Vouchers
	query.Stock
}

type Commands struct {
	CreateOutlet *command.CreateOutletHandler
	UpdateOutlet *command.UpdateOutletHandler
	DeleteOutlet *command.DeleteOutletHandler

	CreateService *command.CreateServiceHandler
	UpdateService *command.UpdateServiceHandler
	DeleteService *command.DeleteServiceHandler

	CreateVariantGroup *command.CreateVariantGroupHandler
	UpdateVariantGroup *command.UpdateVariantGroupHandler
	DeleteVariantGroup *command.DeleteVariantGroupHandler

	CreateModifierGroup *command.CreateModifierGroupHandler
	UpdateModifierGroup *command.UpdateModifierGroupHandler
	DeleteModifierGroup *command.DeleteModifierGroupHandler

	CreateMaterial *command.CreateMaterialHandler
	UpdateMaterial *command.UpdateMaterialHandler
	DeleteMaterial *command.DeleteMaterialHandler
	SetRecipe      *command.SetRecipeHandler

	CreateBanner    *command.CreateBannerHandler
	UpdateBanner    *command.UpdateBannerHandler
	SetBannerStatus *command.SetBannerStatusHandler
	DeleteBanner    *command.DeleteBannerHandler
	ReorderBanners  *command.ReorderBannersHandler

	CreateVoucher    *command.CreateVoucherHandler
	UpdateVoucher    *command.UpdateVoucherHandler
	SetVoucherStatus *command.SetVoucherStatusHandler
	DeleteVoucher    *command.DeleteVoucherHandler
	InjectVoucher    *command.InjectVoucherHandler
	ClaimVoucher     *command.ClaimVoucherHandler

	ReceiveStock  *command.ReceiveStockHandler
	WasteStock    *command.WasteStockHandler
	CountStock    *command.CountStockHandler
	TransferStock *command.TransferStockHandler
	SetMinStock   *command.SetMinStockHandler
	// ConsumeOrder and ReverseOrder are called by the order service, never by a person.
	ConsumeOrder *command.ConsumeOrderHandler
	ReverseOrder *command.ReverseOrderHandler
}

type Queries struct {
	ListOutlets *query.ListOutletsHandler
	PageOutlets *query.PageOutletsHandler
	GetOutlet   *query.GetOutletHandler

	// The customer app's side, read through Discovery.
	NearbyOutlets    *query.NearbyOutletsHandler
	ResolveCoverage  *query.ResolveCoverageHandler
	BrowseServices   *query.BrowseServicesHandler
	GetServiceDetail *query.GetServiceDetailHandler

	ListServices          *query.ListServicesHandler
	PageServices          *query.PageServicesHandler
	ListServiceCategories *query.ListServiceCategoriesHandler
	ListTaxRates          *query.ListTaxRatesHandler

	ListVariantGroups  *query.ListVariantGroupsHandler
	GetVariantGroup    *query.GetVariantGroupHandler
	ListModifierGroups *query.ListModifierGroupsHandler
	GetModifierGroup   *query.GetModifierGroupHandler

	ListMaterials     *query.ListMaterialsHandler
	GetMaterial       *query.GetMaterialHandler
	GetRecipe         *query.GetRecipeHandler
	GetServiceCosting *query.GetServiceCostingHandler
	CalcCosting       *query.CalcCostingHandler

	ListBanners     *query.ListBannersHandler
	GetBanner       *query.GetBannerHandler
	ListLiveBanners *query.ListLiveBannersHandler

	ListVouchers      *query.ListVouchersHandler
	GetVoucher        *query.GetVoucherHandler
	ListVoucherGrants *query.ListVoucherGrantsHandler
	ListInjections    *query.ListInjectionsHandler
	ListWallet        *query.ListWalletHandler

	ListStock     *query.ListStockHandler
	ListMovements *query.ListMovementsHandler
}

type App struct {
	Commands Commands
	Queries  Queries
}

// Discovery is what adapters/postgres provides for the customer app: outlets and the service menu.
type Discovery interface {
	query.OutletDiscovery
	query.ServiceDiscovery
}

func New(store Store, discovery Discovery) *App {
	return &App{
		Commands: Commands{
			CreateOutlet: command.NewCreateOutletHandler(store),
			UpdateOutlet: command.NewUpdateOutletHandler(store),
			DeleteOutlet: command.NewDeleteOutletHandler(store),

			CreateService: command.NewCreateServiceHandler(store),
			UpdateService: command.NewUpdateServiceHandler(store),
			DeleteService: command.NewDeleteServiceHandler(store),

			CreateVariantGroup: command.NewCreateVariantGroupHandler(store),
			UpdateVariantGroup: command.NewUpdateVariantGroupHandler(store),
			DeleteVariantGroup: command.NewDeleteVariantGroupHandler(store),

			CreateModifierGroup: command.NewCreateModifierGroupHandler(store),
			UpdateModifierGroup: command.NewUpdateModifierGroupHandler(store),
			DeleteModifierGroup: command.NewDeleteModifierGroupHandler(store),

			CreateMaterial: command.NewCreateMaterialHandler(store),
			UpdateMaterial: command.NewUpdateMaterialHandler(store),
			DeleteMaterial: command.NewDeleteMaterialHandler(store),
			SetRecipe:      command.NewSetRecipeHandler(store),

			CreateBanner:    command.NewCreateBannerHandler(store),
			UpdateBanner:    command.NewUpdateBannerHandler(store),
			SetBannerStatus: command.NewSetBannerStatusHandler(store),
			DeleteBanner:    command.NewDeleteBannerHandler(store),
			ReorderBanners:  command.NewReorderBannersHandler(store),

			CreateVoucher:    command.NewCreateVoucherHandler(store),
			UpdateVoucher:    command.NewUpdateVoucherHandler(store),
			SetVoucherStatus: command.NewSetVoucherStatusHandler(store),
			DeleteVoucher:    command.NewDeleteVoucherHandler(store),
			InjectVoucher:    command.NewInjectVoucherHandler(store),
			ClaimVoucher:     command.NewClaimVoucherHandler(store),

			ReceiveStock:  command.NewReceiveStockHandler(store),
			WasteStock:    command.NewWasteStockHandler(store),
			CountStock:    command.NewCountStockHandler(store),
			TransferStock: command.NewTransferStockHandler(store),
			SetMinStock:   command.NewSetMinStockHandler(store),
			ConsumeOrder:  command.NewConsumeOrderHandler(store),
			ReverseOrder:  command.NewReverseOrderHandler(store),
		},
		Queries: Queries{
			ListOutlets: query.NewListOutletsHandler(store),
			PageOutlets: query.NewPageOutletsHandler(store),
			GetOutlet:   query.NewGetOutletHandler(store),

			NearbyOutlets:    query.NewNearbyOutletsHandler(discovery),
			ResolveCoverage:  query.NewResolveCoverageHandler(discovery),
			BrowseServices:   query.NewBrowseServicesHandler(discovery),
			GetServiceDetail: query.NewGetServiceDetailHandler(discovery),

			ListServices:          query.NewListServicesHandler(store),
			PageServices:          query.NewPageServicesHandler(store),
			ListServiceCategories: query.NewListServiceCategoriesHandler(store),
			ListTaxRates:          query.NewListTaxRatesHandler(store),

			ListVariantGroups:  query.NewListVariantGroupsHandler(store),
			GetVariantGroup:    query.NewGetVariantGroupHandler(store),
			ListModifierGroups: query.NewListModifierGroupsHandler(store),
			GetModifierGroup:   query.NewGetModifierGroupHandler(store),

			ListMaterials:     query.NewListMaterialsHandler(store),
			GetMaterial:       query.NewGetMaterialHandler(store),
			GetRecipe:         query.NewGetRecipeHandler(store),
			GetServiceCosting: query.NewGetServiceCostingHandler(store),
			CalcCosting:       query.NewCalcCostingHandler(store),

			ListBanners:     query.NewListBannersHandler(store),
			GetBanner:       query.NewGetBannerHandler(store),
			ListLiveBanners: query.NewListLiveBannersHandler(store),

			ListVouchers:      query.NewListVouchersHandler(store),
			GetVoucher:        query.NewGetVoucherHandler(store),
			ListVoucherGrants: query.NewListVoucherGrantsHandler(store),
			ListInjections:    query.NewListInjectionsHandler(store),
			ListWallet:        query.NewListWalletHandler(store),

			ListStock:     query.NewListStockHandler(store),
			ListMovements: query.NewListMovementsHandler(store),
		},
	}
}
