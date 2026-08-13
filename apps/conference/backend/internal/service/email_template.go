// Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package service

import (
	"fmt"
	"strings"
	"wso2-coin-backend/internal/models"
)

// GenerateOrderConfirmationEmail generates the HTML template for an order confirmation email.
func GenerateOrderConfirmationEmail(order models.ShopOrder, recipientName string) string {
	var itemsHtml string
	for _, item := range order.Items {
		itemsHtml += fmt.Sprintf(`
            <tr>
                <td style="padding: 10px; border-bottom: 1px solid #ddd;">%s</td>
                <td style="padding: 10px; text-align: center; border-bottom: 1px solid #ddd;">%d</td>
                <td style="padding: 10px; border-bottom: 1px solid #ddd;">%s O2C</td>
            </tr>
        `, escapeHtml(item.ItemName), item.Quantity, formatDecimal(item.UnitPrice))
	}

	statePart := ""
	if order.ShippingState != nil && *order.ShippingState != "" {
		statePart = ", " + escapeHtml(*order.ShippingState)
	}
	postalPart := ""
	if order.ShippingPostalCode != nil && *order.ShippingPostalCode != "" {
		postalPart = " " + escapeHtml(*order.ShippingPostalCode)
	}

	addressHtml := escapeHtml(order.ShippingAddressLine1) + ",<br/>"
	if order.ShippingAddressLine2 != nil && *order.ShippingAddressLine2 != "" {
		addressHtml += escapeHtml(*order.ShippingAddressLine2) + ",<br/>"
	}
	addressHtml += escapeHtml(order.ShippingCity) + statePart + postalPart + ",<br/>"
	addressHtml += escapeHtml(order.ShippingCountry)

	return fmt.Sprintf(`
    <div style="font-family: Arial, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px; border: 1px solid #eaeaea; border-radius: 8px;">
        <h2 style="color: #ff5000;">Order Confirmed!</h2>
        <p>Hello %s,</p>
        <p>Your order has been successfully confirmed. Thank you for your purchase!</p>
        <div style="background-color: #f9f9f9; padding: 15px; border-radius: 5px; margin: 20px 0;">
            <p style="margin: 0; font-size: 16px;"><strong>Order ID:</strong> %s</p>
        </div>
        
        <h3>Order Summary</h3>
        <table style="width: 100%%; border-collapse: collapse; margin-bottom: 20px;">
            <thead>
                <tr style="background-color: #f1f1f1;">
                    <th style="padding: 10px; text-align: left; border-bottom: 2px solid #ddd;">Item</th>
                    <th style="padding: 10px; text-align: center; border-bottom: 2px solid #ddd;">Quantity</th>
                    <th style="padding: 10px; text-align: left; border-bottom: 2px solid #ddd;">Price</th>
                </tr>
            </thead>
            <tbody>
                %s
            </tbody>
        </table>
        
        <p><strong>Total Paid:</strong> %s O2C</p>

        <h3>Shipping Details</h3>
        <div style="background-color: #f9f9f9; padding: 15px; border-radius: 5px; margin-bottom: 20px;">
            <p style="margin: 0;"><strong>Name:</strong> %s</p>
            <p style="margin: 5px 0 0 0;"><strong>Address:</strong></p>
            <p style="margin: 0 0 0 10px; line-height: 1.5;">
                %s
            </p>
        </div>

        <div style="background-color: #e8f4fd; color: #0056b3; padding: 12px; border-left: 4px solid #0056b3; border-radius: 4px; margin-top: 20px;">
            <p style="margin: 0; font-size: 14px;"><strong>Note:</strong> Your items will be shipped to the address provided above.</p>
        </div>

        <p style="margin-top: 30px; font-size: 12px; color: #888;">This is an automated message, please do not reply.</p>
    </div>
    `, recipientName, escapeHtml(order.ID), itemsHtml, formatDecimal(order.TotalCoinsAmount), recipientName, addressHtml)
}

func escapeHtml(input string) string {
	escaped := strings.ReplaceAll(input, "&", "&amp;")
	escaped = strings.ReplaceAll(escaped, "<", "&lt;")
	escaped = strings.ReplaceAll(escaped, ">", "&gt;")
	escaped = strings.ReplaceAll(escaped, "\"", "&quot;")
	escaped = strings.ReplaceAll(escaped, "'", "&#39;")
	return escaped
}

func formatDecimal(val float64) string {
	str := fmt.Sprintf("%.2f", val)
	if strings.Contains(str, ".") {
		str = strings.TrimRight(str, "0")
		if strings.HasSuffix(str, ".") {
			str = strings.TrimRight(str, ".")
		}
	}
	return str
}
