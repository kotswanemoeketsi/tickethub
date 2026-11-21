-- Venue coordinates, used to find events near a buyer. They are optional: the application
-- fills them in from the city for well-known South African cities and towns, and leaves
-- them empty when the place is unknown.

ALTER TABLE venues
  ADD COLUMN latitude DECIMAL(9,6) NULL AFTER postal_code,
  ADD COLUMN longitude DECIMAL(9,6) NULL AFTER latitude,
  ADD KEY idx_venues_location (latitude, longitude),
  ADD KEY idx_venues_province_city (province, city);
