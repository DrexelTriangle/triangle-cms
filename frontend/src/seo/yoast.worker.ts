// Web-worker entry that hosts Yoast's AnalysisWebWorker so the heavy yoastseo
// analysis (and its node-polyfilled deps) runs off the main thread. The main
// thread talks to it through AnalysisWorkerWrapper in useYoastAnalysis.ts.
//
// The node builtins these modules pull in (events / buffer / url) are aliased to
// browser polyfills in vite.config.ts; without that this file crashes at load.
import { Paper } from "yoastseo"
import AnalysisWebWorker from "yoastseo/build/worker/AnalysisWebWorker"
import Researcher from "yoastseo/build/languageProcessing/languages/en/Researcher"

const researcher = new Researcher(new Paper(""))
const worker = new AnalysisWebWorker(self, researcher)
worker.register()
