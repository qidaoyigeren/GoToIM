package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/model"
)

func (s *SQLStore) seedDemoMarket(ctx context.Context) error {
	now := formatTime(time.Now())
	merchants := []*model.Merchant{
		{
			MerchantID:  "m_apple_store",
			MerchantUID: 90001,
			Name:        "数码旗舰店",
			Description: "手机、配件与虚拟权益订单演示商家",
			GroupRoomID: "merchant:m_apple_store:buyers",
			GroupName:   "数码旗舰店买家群",
		},
		{
			MerchantID:  "m_office_supply",
			MerchantUID: 90002,
			Name:        "企业采购中心",
			Description: "办公设备与企业采购订单演示商家",
			GroupRoomID: "merchant:m_office_supply:buyers",
			GroupName:   "企业采购服务群",
		},
		{
			MerchantID:  "m_service_care",
			MerchantUID: 90003,
			Name:        "售后服务站",
			Description: "售后服务单与紧急支持消息演示商家",
			GroupRoomID: "merchant:m_service_care:support",
			GroupName:   "售后服务支持群",
		},
	}
	for _, m := range merchants {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO merchants
			(merchant_id, merchant_uid, name, description, logo_url, group_room_id, group_name, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE merchant_uid = VALUES(merchant_uid), name = VALUES(name),
				description = VALUES(description), group_room_id = VALUES(group_room_id),
				group_name = VALUES(group_name), updated_at = VALUES(updated_at)`,
			m.MerchantID, m.MerchantUID, m.Name, m.Description, m.LogoURL, m.GroupRoomID, m.GroupName, now, now); err != nil {
			return err
		}
		group := &model.MerchantGroup{
			GroupID:     "grp_" + m.MerchantID,
			MerchantID:  m.MerchantID,
			MerchantUID: m.MerchantUID,
			RoomID:      m.GroupRoomID,
			Name:        m.GroupName,
			Description: m.Name + "的订单消息群聊",
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO merchant_groups
			(group_id, merchant_id, merchant_uid, room_id, name, description, member_count, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?)
			ON DUPLICATE KEY UPDATE merchant_uid = VALUES(merchant_uid), room_id = VALUES(room_id),
				name = VALUES(name), description = VALUES(description), updated_at = VALUES(updated_at)`,
			group.GroupID, group.MerchantID, group.MerchantUID, group.RoomID, group.Name, group.Description, now, now); err != nil {
			return err
		}
	}

	products := []*model.Product{
		{ProductID: "p_iphone_case", MerchantID: "m_apple_store", SKUID: "sku_case_clear", Name: "磁吸透明保护壳", Description: "普通商品订单演示", Price: 199, FulfillmentMode: "physical"},
		{ProductID: "p_airpods_service", MerchantID: "m_apple_store", SKUID: "sku_airpods_care", Name: "耳机快速换新服务", Description: "售后服务单演示", Price: 299, FulfillmentMode: "service"},
		{ProductID: "p_cloud_coupon", MerchantID: "m_apple_store", SKUID: "sku_cloud_100", Name: "云空间兑换码", Description: "虚拟商品消息演示", Price: 100, FulfillmentMode: "virtual"},
		{ProductID: "p_laptop_bulk", MerchantID: "m_office_supply", SKUID: "sku_laptop_20", Name: "办公笔记本批量采购", Description: "企业采购订单演示", Price: 5699, FulfillmentMode: "physical"},
		{ProductID: "p_printer_presale", MerchantID: "m_office_supply", SKUID: "sku_printer_pre", Name: "新款打印机预售", Description: "预售订单演示", Price: 1599, FulfillmentMode: "presale"},
		{ProductID: "p_onsite_repair", MerchantID: "m_service_care", SKUID: "sku_repair_onsite", Name: "上门维修服务", Description: "加急售后服务单演示", Price: 399, FulfillmentMode: "service"},
	}
	for _, p := range products {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO products
			(product_id, merchant_id, sku_id, name, description, price, image_url, fulfillment_mode, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE merchant_id = VALUES(merchant_id), sku_id = VALUES(sku_id),
				name = VALUES(name), description = VALUES(description), price = VALUES(price),
				image_url = VALUES(image_url), fulfillment_mode = VALUES(fulfillment_mode), updated_at = VALUES(updated_at)`,
			p.ProductID, p.MerchantID, p.SKUID, p.Name, p.Description, p.Price, p.ImageURL, p.FulfillmentMode, now, now); err != nil {
			return err
		}
	}
	return nil
}

// ListMerchants returns demo merchants for the market picker.
func (s *SQLStore) ListMerchants() ([]*model.Merchant, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT merchant_id, merchant_uid, name, COALESCE(description, ''),
		COALESCE(logo_url, ''), COALESCE(group_room_id, ''), COALESCE(group_name, ''), created_at, updated_at
		FROM merchants ORDER BY merchant_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var merchants []*model.Merchant
	for rows.Next() {
		m, err := scanMerchant(rows)
		if err != nil {
			return nil, err
		}
		merchants = append(merchants, m)
	}
	return merchants, rows.Err()
}

// GetMerchant returns a merchant by id.
func (s *SQLStore) GetMerchant(merchantID string) (*model.Merchant, error) {
	row := s.db.QueryRowContext(context.Background(), `SELECT merchant_id, merchant_uid, name, COALESCE(description, ''),
		COALESCE(logo_url, ''), COALESCE(group_room_id, ''), COALESCE(group_name, ''), created_at, updated_at
		FROM merchants WHERE merchant_id = ?`, merchantID)
	return scanMerchant(row)
}

// ListProducts returns demo products. merchantID may be empty.
func (s *SQLStore) ListProducts(merchantID string) ([]*model.Product, error) {
	query := `SELECT product_id, merchant_id, COALESCE(sku_id, ''), name, COALESCE(description, ''),
		price, COALESCE(image_url, ''), COALESCE(fulfillment_mode, ''), created_at, updated_at FROM products`
	var args []any
	if merchantID != "" {
		query += ` WHERE merchant_id = ?`
		args = append(args, merchantID)
	}
	query += ` ORDER BY merchant_id, product_id`
	rows, err := s.db.QueryContext(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var products []*model.Product
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, err
		}
		products = append(products, p)
	}
	return products, rows.Err()
}

// GetProduct returns one demo product.
func (s *SQLStore) GetProduct(productID string) (*model.Product, error) {
	row := s.db.QueryRowContext(context.Background(), `SELECT product_id, merchant_id, COALESCE(sku_id, ''), name,
		COALESCE(description, ''), price, COALESCE(image_url, ''), COALESCE(fulfillment_mode, ''), created_at, updated_at
		FROM products WHERE product_id = ?`, productID)
	return scanProduct(row)
}

// GetMerchantGroupByRoomID returns a merchant group by room id.
func (s *SQLStore) GetMerchantGroupByRoomID(roomID string) (*model.MerchantGroup, error) {
	row := s.db.QueryRowContext(context.Background(), `SELECT group_id, merchant_id, merchant_uid, room_id, name,
		COALESCE(description, ''), member_count, created_at, updated_at FROM merchant_groups WHERE room_id = ?`, roomID)
	return scanMerchantGroup(row)
}

// ListMerchantGroups returns all groups for a merchant.
func (s *SQLStore) ListMerchantGroups(merchantID string) ([]*model.MerchantGroup, error) {
	query := `SELECT group_id, merchant_id, merchant_uid, room_id, name, COALESCE(description, ''),
		member_count, created_at, updated_at FROM merchant_groups`
	var args []any
	if merchantID != "" {
		query += ` WHERE merchant_id = ?`
		args = append(args, merchantID)
	}
	query += ` ORDER BY merchant_id, group_id`
	rows, err := s.db.QueryContext(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []*model.MerchantGroup
	for rows.Next() {
		g, err := scanMerchantGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func scanMerchant(row scanner) (*model.Merchant, error) {
	var m model.Merchant
	var created, updated string
	if err := row.Scan(&m.MerchantID, &m.MerchantUID, &m.Name, &m.Description, &m.LogoURL,
		&m.GroupRoomID, &m.GroupName, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	m.CreatedAt, _ = parseTime(created)
	m.UpdatedAt, _ = parseTime(updated)
	return &m, nil
}

func scanProduct(row scanner) (*model.Product, error) {
	var p model.Product
	var created, updated string
	if err := row.Scan(&p.ProductID, &p.MerchantID, &p.SKUID, &p.Name, &p.Description,
		&p.Price, &p.ImageURL, &p.FulfillmentMode, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.CreatedAt, _ = parseTime(created)
	p.UpdatedAt, _ = parseTime(updated)
	return &p, nil
}

func scanMerchantGroup(row scanner) (*model.MerchantGroup, error) {
	var g model.MerchantGroup
	var created, updated string
	if err := row.Scan(&g.GroupID, &g.MerchantID, &g.MerchantUID, &g.RoomID, &g.Name,
		&g.Description, &g.MemberCount, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	g.CreatedAt, _ = parseTime(created)
	g.UpdatedAt, _ = parseTime(updated)
	return &g, nil
}
