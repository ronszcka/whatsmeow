// Copyright (c) 2026 BiaZap (fork patch #11 — WhatsApp Business catalog API)
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
//
// This file is a faithful Go port of WhiskeySockets/Baileys' catalog IQ
// queries (src/Socket/business.ts + src/Utils/business.ts). Upstream
// whatsmeow exposes NO catalog API; Baileys is the industry reference for
// the unofficial multi-device WEB client. Every IQ here mirrors Baileys'
// node structure exactly.
//
// Ref (Baileys): src/Socket/business.ts getCatalog/getCollections/productCreate/productUpdate/productDelete
// Ref (Baileys): src/Utils/business.ts toProductNode/parseProductNode/parseCatalogNode/parseCollectionsNode
// Ref (whatsmeow): user.go GetBusinessProfile (the sendIQ + node-walk template)

package whatsmeow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/socket"
	"go.mau.fi/whatsmeow/types"
)

const (
	// catalogNamespace is the IQ xmlns for catalog read/write queries.
	catalogNamespace = "w:biz:catalog"
	// productImageDim is the requested thumbnail dimension (Baileys uses 100x100).
	productImageDim = "100"
	// productImageUploadPath is the media-host path for catalog product image
	// uploads. UNLIKE message media (which uses /mms/{type}), catalog images
	// post to /product/image and are UNencrypted (the catalog node references
	// them by direct-path URL). Mirrors Baileys MEDIA_PATH_MAP['product-catalog-image'].
	productImageUploadPath = "/product/image"
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

// ProductCreate adds a product to your own WhatsApp Business catalog. Any
// images in the input must already be uploaded (DirectPath set, e.g. via
// UploadProductImage).
//
// Ref (Baileys): src/Socket/business.ts productCreate
func (cli *Client) ProductCreate(ctx context.Context, product types.ProductInput) (*types.Product, error) {
	return cli.productMutate(ctx, "product_catalog_add", "", product)
}

// ProductUpdate edits an existing product (by ID) in your own catalog.
//
// Ref (Baileys): src/Socket/business.ts productUpdate
func (cli *Client) ProductUpdate(ctx context.Context, productID string, product types.ProductInput) (*types.Product, error) {
	if productID == "" {
		return nil, fmt.Errorf("productID is required for update")
	}
	return cli.productMutate(ctx, "product_catalog_edit", productID, product)
}

func (cli *Client) productMutate(ctx context.Context, wrapperTag, productID string, product types.ProductInput) (*types.Product, error) {
	if cli == nil {
		return nil, ErrClientIsNil
	}
	resp, err := cli.sendIQ(ctx, infoQuery{
		Namespace: catalogNamespace,
		Type:      iqSet,
		To:        types.ServerJID,
		Content: []waBinary.Node{{
			Tag:   wrapperTag,
			Attrs: waBinary.Attrs{"v": "1"},
			Content: []waBinary.Node{
				toProductNode(productID, product),
				{Tag: "width", Content: []byte(productImageDim)},
				{Tag: "height", Content: []byte(productImageDim)},
			},
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to %s product: %w", wrapperTag, err)
	}
	wrapper, ok := resp.GetOptionalChildByTag(wrapperTag)
	if !ok {
		return nil, fmt.Errorf("missing <%s> in response", wrapperTag)
	}
	productNode, ok := wrapper.GetOptionalChildByTag("product")
	if !ok {
		return nil, fmt.Errorf("missing <product> in %s response", wrapperTag)
	}
	p := parseProductNode(productNode)
	return &p, nil
}

// ProductDelete removes products (by ID) from your own catalog. Returns the
// number of products WhatsApp reports as deleted.
//
// Ref (Baileys): src/Socket/business.ts productDelete
func (cli *Client) ProductDelete(ctx context.Context, productIDs []string) (int, error) {
	if cli == nil {
		return 0, ErrClientIsNil
	}
	if len(productIDs) == 0 {
		return 0, nil
	}
	productNodes := make([]waBinary.Node, len(productIDs))
	for i, id := range productIDs {
		productNodes[i] = waBinary.Node{
			Tag:     "product",
			Content: []waBinary.Node{{Tag: "id", Content: []byte(id)}},
		}
	}
	resp, err := cli.sendIQ(ctx, infoQuery{
		Namespace: catalogNamespace,
		Type:      iqSet,
		To:        types.ServerJID,
		Content: []waBinary.Node{{
			Tag:     "product_catalog_delete",
			Attrs:   waBinary.Attrs{"v": "1"},
			Content: productNodes,
		}},
	})
	if err != nil {
		return 0, fmt.Errorf("failed to delete products: %w", err)
	}
	delNode, ok := resp.GetOptionalChildByTag("product_catalog_delete")
	if !ok {
		return 0, nil
	}
	count, _ := delNode.AttrGetter().GetInt64("deleted_count", false)
	return int(count), nil
}

// UploadProductImage uploads a catalog product image to WhatsApp's media
// servers and returns the result with DirectPath/URL set. Catalog images are
// uploaded UNencrypted (the catalog node references them by direct-path URL,
// unlike message media which is E2E encrypted), and to a DIFFERENT path
// (/product/image, not /mms/...). The returned DirectPath feeds
// types.ProductImage.DirectPath for ProductCreate/Update.
//
// The upload goes through cli.mediaHTTP, which on BiaZap is bound to the
// instance's proxy — so this byte path obeys the proxy/VPN invariant.
//
// Ref (Baileys): src/Utils/business.ts uploadingNecessaryImages + src/Utils/messages-media.ts getWAUploadToServer (mediaType "product-catalog-image", MEDIA_PATH_MAP "/product/image", encodeBase64EncodedStringForUpload)
func (cli *Client) UploadProductImage(ctx context.Context, data []byte) (resp UploadResponse, err error) {
	if cli == nil {
		return resp, ErrClientIsNil
	}
	if len(data) == 0 {
		return resp, fmt.Errorf("empty product image")
	}
	sum := sha256.Sum256(data)
	resp.FileSHA256 = sum[:]
	resp.FileLength = uint64(len(data))
	// Baileys: digest('base64') then encodeBase64EncodedStringForUpload
	// (+→-, /→_, strip padding) == base64url without padding.
	token := base64.RawURLEncoding.EncodeToString(sum[:])

	mediaConn, err := cli.refreshMediaConn(ctx, false)
	if err != nil {
		return resp, fmt.Errorf("failed to refresh media connections: %w", err)
	}

	var lastErr error
	for _, host := range mediaConn.Hosts {
		uploadURL := url.URL{
			Scheme:   "https",
			Host:     host.Hostname,
			Path:     fmt.Sprintf("%s/%s", productImageUploadPath, token),
			RawQuery: url.Values{"auth": {mediaConn.Auth}, "token": {token}}.Encode(),
		}
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL.String(), bytes.NewReader(data))
		if reqErr != nil {
			return resp, fmt.Errorf("failed to prepare product image upload: %w", reqErr)
		}
		req.ContentLength = int64(len(data))
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Origin", socket.Origin)
		req.Header.Set("Referer", socket.Origin+"/")

		httpResp, doErr := cli.mediaHTTP.Do(req)
		if doErr != nil {
			lastErr = fmt.Errorf("upload to %s failed: %w", host.Hostname, doErr)
			continue
		}
		if httpResp.StatusCode != http.StatusOK {
			_ = httpResp.Body.Close()
			lastErr = fmt.Errorf("upload to %s returned status %d", host.Hostname, httpResp.StatusCode)
			continue
		}
		decErr := json.NewDecoder(httpResp.Body).Decode(&resp)
		_ = httpResp.Body.Close()
		if decErr != nil {
			lastErr = fmt.Errorf("failed to parse product image upload response: %w", decErr)
			continue
		}
		if resp.DirectPath == "" && resp.URL == "" {
			lastErr = fmt.Errorf("product image upload to %s returned no direct_path", host.Hostname)
			continue
		}
		return resp, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no media hosts available")
	}
	return resp, lastErr
}

// toProductNode builds the <product> binary node for create/update.
//
// Ref (Baileys): src/Utils/business.ts toProductNode
func toProductNode(productID string, product types.ProductInput) waBinary.Node {
	attrs := waBinary.Attrs{}
	var content []waBinary.Node

	if productID != "" {
		content = append(content, waBinary.Node{Tag: "id", Content: []byte(productID)})
	}
	if product.Name != "" {
		content = append(content, waBinary.Node{Tag: "name", Content: []byte(product.Name)})
	}
	if product.Description != "" {
		content = append(content, waBinary.Node{Tag: "description", Content: []byte(product.Description)})
	}
	if product.RetailerID != "" {
		content = append(content, waBinary.Node{Tag: "retailer_id", Content: []byte(product.RetailerID)})
	}
	if len(product.Images) > 0 {
		imageNodes := make([]waBinary.Node, 0, len(product.Images))
		for _, img := range product.Images {
			url := img.DirectPath
			if url != "" && url[0] == '/' {
				url = mediaDirectPathHost + url
			}
			imageNodes = append(imageNodes, waBinary.Node{
				Tag:     "image",
				Content: []waBinary.Node{{Tag: "url", Content: []byte(url)}},
			})
		}
		content = append(content, waBinary.Node{Tag: "media", Content: imageNodes})
	}
	if product.Price != 0 {
		content = append(content, waBinary.Node{Tag: "price", Content: []byte(strconv.FormatInt(product.Price, 10))})
	}
	if product.Currency != "" {
		content = append(content, waBinary.Node{Tag: "currency", Content: []byte(product.Currency)})
	}
	if product.OriginCountryCode == "" {
		attrs["compliance_category"] = "COUNTRY_ORIGIN_EXEMPT"
	} else {
		content = append(content, waBinary.Node{
			Tag:     "compliance_info",
			Content: []waBinary.Node{{Tag: "country_code_origin", Content: []byte(product.OriginCountryCode)}},
		})
	}
	if product.IsHidden {
		attrs["is_hidden"] = "true"
	}
	return waBinary.Node{Tag: "product", Attrs: attrs, Content: content}
}

// mediaDirectPathHost is the WhatsApp media CDN host used to build a full URL
// from a direct path (Baileys DEF_MEDIA_HOST).
const mediaDirectPathHost = "https://mmg.whatsapp.net"

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
