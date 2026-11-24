# Ticket fees

Standard pricing is 3.5% of each discounted paid ticket price plus R5 per paid ticket, deducted from organiser proceeds. No extra buyer surcharge is introduced. These amounts are not increased by an additional VAT calculation in the application.

Commission is rounded to the nearest cent per ticket. Free, fully discounted and complimentary tickets carry neither fee. A paid ticket priced below the total fee can produce negative organiser earnings; organisers should price tickets accordingly.

Migration 0006 sets existing organisers to 350 basis points for future sales. Administrators may still negotiate percentage overrides. New registrations default to 350 unless DEFAULT_COMMISSION_BPS overrides it. Remove any old 500 override at rollout. The fixed fee is R5.

New orders and tickets snapshot both fee components at fulfilment. Historical sales retain their original percentage-only charges. Completed ticket refunds reverse the original ticket commission and fixed fee on the refund date. Duplicate-payment refunds do not reverse ticket fees. Both revenue and organiser-performance reports show commission, ticket fees, total fees and organiser earnings, including CSV, Excel and PDF exports.

Deploy the application and run the bundled migrations together. Do not run the new binary against the old schema. This migration has not been applied to production by this code change.
