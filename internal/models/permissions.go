package models

type PermissionDescriptor struct {
	Name        string
	Description string
}

func PermissionDescriptors() []PermissionDescriptor {
	return []PermissionDescriptor{
		{"warehouse.view_overview", "Dashboard / bootstrap"},
		{"warehouse.view_location", "View facilities, zones, bins"},
		{"warehouse.add_location", "Create facilities, zones, bins"},
		{"warehouse.change_location", "Update facilities, zones, bins"},
		{"warehouse.view_item", "View item master"},
		{"warehouse.add_item", "Create items"},
		{"warehouse.change_item", "Update items"},
		{"warehouse.view_stock", "View balances and low stock"},
		{"warehouse.view_receipt", "View receipts"},
		{"warehouse.add_receipt", "Create receipts"},
		{"warehouse.post_receipt", "Post receipts to stock"},
		{"warehouse.view_issue", "View issues"},
		{"warehouse.add_issue", "Create issues"},
		{"warehouse.post_issue", "Post issues from stock"},
		{"warehouse.issue_consumable", "Issue consumables to departments"},
		{"warehouse.production_consume", "Production RM consumption"},
		{"warehouse.production_output", "Production FG output"},
		{"warehouse.add_transfer", "Create bin transfers"},
		{"warehouse.adjust_stock", "Stock adjustments"},
		{"warehouse.cycle_count", "Cycle count adjustments"},
		{"warehouse.view_asset", "View equipment assets"},
		{"warehouse.add_asset", "Register equipment assets"},
		{"warehouse.checkin_asset", "Check in equipment to bin"},
		{"warehouse.checkout_asset", "Check out equipment custody"},
		{"warehouse.add_pick", "Create pick lists"},
		{"warehouse.confirm_pick", "Confirm pick lists"},
		{"warehouse.add_pack", "Create pack sessions"},
		{"warehouse.admin.read", "Staff audit and monitoring"},
	}
}
