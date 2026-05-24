// Copyright (c) 2026 BiaZap (fork patch #11 — WhatsApp Business catalog READ API)
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
//
// This file is a faithful Go port of WhiskeySockets/Baileys' catalog READ IQ
// queries (src/Socket/business.ts + src/Utils/business.ts). Upstream
// whatsmeow exposes NO catalog API; Baileys is the industry reference for
// the unofficial multi-device WEB client. Every IQ here mirrors Baileys'
// node structure exactly.
//
// SCOPE — READ ONLY. The write path (ProductCreate/Update/Delete +
// UploadProductImage) was implemented and validated up to WhatsApp's server,
// but `product_catalog_add`/`_edit` (type=set) IQs hang with no response that
// whatsmeow's request matcher recognizes — even on a commerce-enabled account
// (Catcher 3, 2026-05-24). The READ IQs (type=get) work through the same
// sendIQ path. Resolving the write path needs a byte-level capture of a live
// Baileys productCreate exchange to find the structural difference; until
// then the write methods are intentionally NOT ported. See
// `.claude/rules/whatsmeow-fork.md` patch #11 + lessons.md.
//
// Ref (Baileys): src/Socket/business.ts getCatalog/getCollections
// Ref (Baileys): src/Utils/business.ts parseCatalogNode/parseCollectionsNode/parseProductNode
// Ref (whatsmeow): user.go GetBusinessProfile (the sendIQ + node-walk template)

package whatsmeow

import (
	"context"
	"fmt"
	"strconv"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
)

const (
	// catalogNamespace is the IQ xmlns for catalog read queries.
	catalogNamespace = "w:biz:catalog"
	// productImageDim is the requested thumbnail dimension (Baileys uses 100x100).
	productImageDim = "100"
)

// childString returns the []byte content of the first child with the given
// tag as a string. Mirrors Baileys getBinaryNodeChildString.
func childString(node waBinary.Node, tag string) string {
	child := node.GetChildByTag(tag)
	if b, ok := child.Content.([]byte); ok {
		return string(b)
	}
	return ""
}

// GetCatalog fetches a page of products from a WhatsApp Business catalog.
//
// jid is the business account JID; pass an empty JID to query your own
// catalog. limit caps the page size (Baileys defaults to 10 when 0). cursor
// is the NextPageCursor from a previous call (empty for the first page).
//
// The returned product image URLs point at WhatsApp's media CDN. Callers MUST
// proxy-cache them before exposing to consumers (proxy-discipline lesson #31).
//
// Ref (Baileys): src/Socket/business.ts getCatalog
func (cli *Client) GetCatalog(ctx context.Context, jid types.JID, limit int, cursor string) (*types.Catalog, error) {
	if cli == nil {
		return nil, ErrClientIsNil
	}
	if jid.IsEmpty() {
		if cli.Store.ID == nil {
			return nil, ErrNotLoggedIn
		}
		jid = cli.Store.ID.ToNonAD()
	} else {
		jid = jid.ToNonAD()
	}
	if limit <= 0 {
		limit = 10
	}

	params := []waBinary.Node{
		{Tag: "limit", Content: []byte(strconv.Itoa(limit))},
		{Tag: "width", Content: []byte(productImageDim)},
		{Tag: "height", Content: []byte(productImageDim)},
	}
	if cursor != "" {
		params = append(params, waBinary.Node{Tag: "after", Content: []byte(cursor)})
	}

	resp, err := cli.sendIQ(ctx, infoQuery{
		Namespace: catalogNamespace,
		Type:      iqGet,
		To:        types.ServerJID,
		Content: []waBinary.Node{{
			Tag: "product_catalog",
			Attrs: waBinary.Attrs{
				"jid":               jid,
				"allow_shop_source": "true",
			},
			Content: params,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query catalog: %w", err)
	}
	return parseCatalogNode(resp), nil
}

// GetCollections fetches the product collections of a WhatsApp Business
// catalog. jid empty queries your own collections. limit caps both the
// number of collections and items per collection (Baileys default 51).
//
// Ref (Baileys): src/Socket/business.ts getCollections
func (cli *Client) GetCollections(ctx context.Context, jid types.JID, limit int) (*types.Collections, error) {
	if cli == nil {
		return nil, ErrClientIsNil
	}
	if jid.IsEmpty() {
		if cli.Store.ID == nil {
			return nil, ErrNotLoggedIn
		}
		jid = cli.Store.ID.ToNonAD()
	} else {
		jid = jid.ToNonAD()
	}
	if limit <= 0 {
		limit = 51
	}
	limStr := strconv.Itoa(limit)

	resp, err := cli.sendIQ(ctx, infoQuery{
		Namespace: catalogNamespace,
		Type:      iqGet,
		To:        types.ServerJID,
		SMaxID:    "35",
		Content: []waBinary.Node{{
			Tag:   "collections",
			Attrs: waBinary.Attrs{"biz_jid": jid},
			Content: []waBinary.Node{
				{Tag: "collection_limit", Content: []byte(limStr)},
				{Tag: "item_limit", Content: []byte(limStr)},
				{Tag: "width", Content: []byte(productImageDim)},
				{Tag: "height", Content: []byte(productImageDim)},
			},
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query collections: %w", err)
	}
	return parseCollectionsNode(resp), nil
}

// parseCatalogNode parses a product_catalog IQ response.
//
// Ref (Baileys): src/Utils/business.ts parseCatalogNode
func parseCatalogNode(node *waBinary.Node) *types.Catalog {
	catalogNode := node.GetChildByTag("product_catalog")
	productNodes := catalogNode.GetChildrenByTag("product")
	products := make([]types.Product, 0, len(productNodes))
	for _, pn := range productNodes {
		products = append(products, parseProductNode(pn))
	}
	cursor := ""
	if paging, ok := catalogNode.GetOptionalChildByTag("paging"); ok {
		cursor = childString(paging, "after")
	}
	return &types.Catalog{Products: products, NextPageCursor: cursor}
}

// parseCollectionsNode parses a collections IQ response.
//
// Ref (Baileys): src/Utils/business.ts parseCollectionsNode
func parseCollectionsNode(node *waBinary.Node) *types.Collections {
	collectionsNode := node.GetChildByTag("collections")
	collNodes := collectionsNode.GetChildrenByTag("collection")
	collections := make([]types.CatalogCollection, 0, len(collNodes))
	for _, cn := range collNodes {
		productNodes := cn.GetChildrenByTag("product")
		products := make([]types.Product, 0, len(productNodes))
		for _, pn := range productNodes {
			products = append(products, parseProductNode(pn))
		}
		status, canAppeal := parseStatusInfo(cn)
		collections = append(collections, types.CatalogCollection{
			ID:        childString(cn, "id"),
			Name:      childString(cn, "name"),
			Status:    status,
			CanAppeal: canAppeal,
			Products:  products,
		})
	}
	return &types.Collections{Collections: collections}
}

// parseProductNode parses a single <product> node.
//
// Ref (Baileys): src/Utils/business.ts parseProductNode
func parseProductNode(productNode waBinary.Node) types.Product {
	price, _ := strconv.ParseInt(childString(productNode, "price"), 10, 64)
	mediaNode := productNode.GetChildByTag("media")
	reviewStatus := ""
	if statusInfo, ok := productNode.GetOptionalChildByTag("status_info"); ok {
		reviewStatus = childString(statusInfo, "status")
	}
	return types.Product{
		ID:           childString(productNode, "id"),
		Name:         childString(productNode, "name"),
		Description:  childString(productNode, "description"),
		RetailerID:   childString(productNode, "retailer_id"),
		URL:          childString(productNode, "url"),
		Price:        price,
		Currency:     childString(productNode, "currency"),
		IsHidden:     productNode.AttrGetter().OptionalString("is_hidden") == "true",
		ReviewStatus: reviewStatus,
		Images:       parseProductImages(mediaNode),
	}
}

// parseProductImages extracts the image URLs from a product's <media> node.
//
// Ref (Baileys): src/Utils/business.ts parseImageUrls
func parseProductImages(mediaNode waBinary.Node) []types.ProductImage {
	imageNodes := mediaNode.GetChildrenByTag("image")
	images := make([]types.ProductImage, 0, len(imageNodes))
	for _, img := range imageNodes {
		images = append(images, types.ProductImage{
			RequestURL:  childString(img, "request_image_url"),
			OriginalURL: childString(img, "original_image_url"),
		})
	}
	return images
}

// parseStatusInfo extracts the review status of a collection.
//
// Ref (Baileys): src/Utils/business.ts parseStatusInfo
func parseStatusInfo(node waBinary.Node) (status string, canAppeal bool) {
	si, ok := node.GetOptionalChildByTag("status_info")
	if !ok {
		return "", false
	}
	return childString(si, "status"), childString(si, "can_appeal") == "true"
}
