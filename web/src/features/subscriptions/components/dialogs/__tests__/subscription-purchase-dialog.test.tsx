/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";

import { subscriptionPlanSchema } from "../../../types";
import { SubscriptionPurchaseDialog } from "../subscription-purchase-dialog";

it("passes the selected Epay method to scoped checkout", async () => {
  const user = userEvent.setup();
  const onPay = vi.fn().mockResolvedValue(undefined);
  const client = new QueryClient();
  const view = render(
    <QueryClientProvider client={client}>
      <SubscriptionPurchaseDialog
        open
        onOpenChange={vi.fn()}
        onPay={onPay}
        enableOnlineTopUp
        epayMethods={[
          { type: "alipay", name: "Alipay" },
          { type: "wxpay", name: "WeChat" },
        ]}
        plan={{
          plan: subscriptionPlanSchema.parse({
            id: 1,
            title: "App plan",
            price_amount: 1,
            currency: "CNY",
            duration_unit: "month",
            duration_value: 1,
            quota_reset_period: "never",
            enabled: true,
            sort_order: 0,
            max_purchase_per_user: 0,
            total_amount: 1000,
          }),
        }}
      />
    </QueryClientProvider>,
  );
  try {
    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByRole("option", { name: "WeChat" }));
    await user.click(screen.getByRole("button", { name: /^Pay$/ }));
    expect(onPay).toHaveBeenCalledWith("wxpay");
  } finally {
    view.unmount();
    client.clear();
  }
});
