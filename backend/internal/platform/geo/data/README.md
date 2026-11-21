# South African cities and towns

`SouthAfricanCities.json` is the list of South African cities, towns and settlements from
[dirkstrauss/SouthAfrica](https://github.com/dirkstrauss/SouthAfrica/blob/master/SouthAfricanCities.json),
© 2017 Dirk Strauss, used under the MIT licence in `LICENSE`. It was downloaded on
20 November 2026 and is embedded unchanged.

When the application loads it (see `../geo.go`), it:

- writes "KwaZulu Natal" as the official "KwaZulu-Natal";
- uses current official names for towns renamed since 2017 (Port Elizabeth is Gqeberha,
  Nelspruit is Mbombela, and so on), keeping the old names as search aliases;
- adds a few well-known places the list lacks, such as Umhlanga, Ballito and Fourways;
- keeps one entry when a name appears twice in the same province.

To update the list, replace the JSON file with a newer copy in the same format and run
`go test ./internal/platform/geo/`.
