// Mutation testing: checks whether the unit tests actually catch bugs, not just
// whether they run. Mutants in files no test touches show up as "no coverage"
// and cost nothing. Run with `npm run test:mutation`; HTML report in reports/mutation/.
/** @type {import('@stryker-mutator/api/core').PartialStrykerOptions} */
export default {
  testRunner: "vitest",
  plugins: ["@stryker-mutator/vitest-runner"],
  mutate: [
    "src/**/*.{ts,tsx}",
    "!src/**/*.test.{ts,tsx}",
    "!src/test/**",
    "!src/main.tsx",
    "!src/**/*.d.ts",
  ],
  coverageAnalysis: "perTest",
  ignoreStatic: true,
  reporters: ["clear-text", "html", "json"],
  htmlReporter: { fileName: "reports/mutation/index.html" },
  jsonReporter: { fileName: "reports/mutation/mutation.json" },
  // Report only; the weekly CI job never fails on score.
  thresholds: { high: 80, low: 60, break: null },
  concurrency: 4,
  timeoutMS: 20000,
}
