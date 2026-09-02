package browser

import "fmt"

// buildIdentityHardeningScript implements #101 stages 3–4 + the
// getter-toString nuance. Design principles (from the PR #102 review):
//   - override PROTOTYPE getters, never instance-level own properties
//     (hasOwnProperty shape checks flag the latter);
//   - assert native descriptor shape in tests, not just values;
//   - disguise override functions' toString() so a detector reading
//     Function.prototype.toString sees native code, not an arrow function.
//
// It is appended to the combined stealth script (self-contained IIFE).
func buildIdentityHardeningScript(profile BrowserProfile) string {
	return fmt.Sprintf(`
		(() => {
			// ---- Native-looking toString for all overrides (the "nuance") ----
			// fp-collect walks Navigator.prototype and prints each getter's
			// source via Function.prototype.toString. A naked override reads
			// "() => false" / "function webdriver() { return false; }" — a
			// tell no real Chrome produces. Per-function toString masking is
			// NOT enough (Function.prototype.toString.call(fn) bypasses the
			// own property), so we intercept toString GLOBALLY: every
			// function registered in the disguise map reports its native
			// text, everything else falls through to the real toString.
			const disguised = new WeakMap();
			function disguise(fn, nativeText) {
				try {
					disguised.set(fn, nativeText);
					Object.defineProperty(fn, 'toString', {
						value: function toString() { return nativeText; },
						writable: true, configurable: true, enumerable: false
					});
				} catch (e) {}
			}
			const realToString = Function.prototype.toString;
			Function.prototype.toString = function toString() {
				const mask = disguised.get(this);
				if (mask !== undefined) return mask;
				return realToString.call(this);
			};
			disguise(Function.prototype.toString, 'function toString() { [native code] }');

			// Register the webdriver getter (defined by the main stealth
			// script that runs BEFORE this IIFE) into the disguise map —
			// it cannot register itself because the map does not exist yet
			// when it runs.
			try {
				const wdDesc = Object.getOwnPropertyDescriptor(Navigator.prototype, 'webdriver');
				if (wdDesc && wdDesc.get) {
					disguise(wdDesc.get, 'function get webdriver() { [native code] }');
				}
			} catch (e) {}

			// ---- Stage 3: WebGL context availability ----
			// On Alpine/aarch64 Chromium 149 the ANGLE/Vulkan stack cannot
			// initialize in a container (VK_KHR_surface missing; GL needs an
			// X display), so canvas.getContext('webgl') returns null —
			// "Canvas has no webgl context" is an instant headless flag
			// (no real desktop Chrome looks like that). We cannot conjure a
			// real GPU here; instead we present a COHERENT software
			// implementation: getContext returns a fully functional 2D-based
			// WebGL stand-in whose fingerprint parameters match the profile
			// (vendor/renderer), like antidetect browsers do.
			const WEBGL_VENDOR = %q;
			const WEBGL_RENDERER = %q;

			function fakeGetParameter(parameter) {
				switch (parameter) {
					case 37445: return WEBGL_VENDOR;
					case 37446: return WEBGL_RENDERER;
					case 7936: return WEBGL_VENDOR;   // VENDOR mirrors unmasked on Chrome/ANGLE
					case 7937: return WEBGL_RENDERER;
					case 7938: return 'WebGL 1.0 (OpenGL ES 2.0 Chromium)';
					case 35724: return 'WebGL GLSL ES 1.0 (OpenGL ES GLSL ES 1.0 Chromium)';
					case 3386: return new Int32Array([16384, 16384]);
					case 34076: case 3379: return 16384;
					case 3410: return 16384;
					case 3413: return 16;
					case 35660: return 16;
					case 36347: return 1024;
					case 36348: return 16;
					case 36349: return 224;
					default: return 0;
				}
			}

			// Build the stand-in only once per realm and reuse it for every
			// canvas — a real browser reuses contexts per canvas type too.
			let cachedFakeGL = {};   // keyed by context type, like real per-canvas contexts
			// Native-shape helpers (#101 review warnings): members must be
			// PROTOTYPE getters with native toString, not own data props —
			// a descriptor walk (fp-collect) flags own props instantly.
			function defineNativeGetter(obj, prop, fn) {
				Object.defineProperty(obj, prop, {
					get: fn, set: undefined, enumerable: false, configurable: true
				});
				disguise(fn, 'function get ' + prop + '() { [native code] }');
			}
			function buildFakeWebGL(canvas, ctxType) {
				// Object.create over the NATIVE prototype gives the stand-in
				// the native tag, instanceof=true, and INHERITED named
				// constants (gl.VERSION etc.) — the review caught that a
				// plain object literal has no constants, so the plain
				// fingerprint branch getParameter(gl.VERSION) returned 0.
				// webgl2 contexts must report instanceof WebGL2RenderingContext.
				const nativeProto = (ctxType === 'webgl2' && typeof WebGL2RenderingContext !== 'undefined')
					? WebGL2RenderingContext.prototype : WebGLRenderingContext.prototype;
				const fake = Object.create(nativeProto);
				// 'drawingBufferWidth'/'drawingBufferHeight' are prototype
				// getters on a real context — mirror that shape.
				try {
					defineNativeGetter(fake, 'drawingBufferWidth', function() { return canvas ? canvas.width : 300; });
					defineNativeGetter(fake, 'drawingBufferHeight', function() { return canvas ? canvas.height : 150; });
				} catch (e) {}
				// Real contexts serve methods from the prototype — put the
				// full method set on a private intermediate proto so the
				// own-property list of the fake stays as empty as a real
				// context's, and instanceof/tag still hit the native
				// WebGLRenderingContext.prototype.
				const glProto = Object.create(WebGLRenderingContext.prototype);
				const noop = () => {};
				const methods = {
					getParameter: function(p) { return fakeGetParameter(p); },
					getExtension: function(name) {
						if (name === 'WEBGL_debug_renderer_info') {
							return { UNMASKED_VENDOR_WEBGL: 37445, UNMASKED_RENDERER_WEBGL: 37446 };
						}
						if (name === 'EXT_texture_filter_anisotropic') return { MAX_TEXTURE_MAX_ANISOTROPY_EXT: 3379 };
						return null;
					},
					getSupportedExtensions: function() {
						return ['ANGLE_instanced_arrays','EXT_blend_minmax','EXT_color_buffer_half_float',
							'EXT_float_blend','EXT_frag_depth','EXT_sRGB','EXT_shader_texture_lod',
							'EXT_texture_compression_bptc','EXT_texture_compression_rgtc','EXT_texture_filter_anisotropic',
							'WEBKIT_EXT_texture_filter_anisotropic','OES_element_index_uint','OES_fbo_render_mipmap',
							'OES_standard_derivatives','OES_texture_float','OES_texture_float_linear',
							'OES_texture_half_float','OES_texture_half_float_linear','OES_vertex_array_object',
							'WEBGL_color_buffer_float','WEBGL_compressed_texture_s3tc',
							'WEBGL_compressed_texture_s3tc_srgb','WEBGL_debug_renderer_info',
							'WEBGL_debug_shaders','WEBGL_depth_texture','WEBGL_draw_buffers',
							'WEBGL_lose_context'];
					},
					getShaderPrecisionFormat: function() { return { rangeMin: 127, rangeMax: 127, precision: 23 }; },
					createBuffer: () => ({}), bindBuffer: noop, bufferData: noop, deleteBuffer: noop,
					createTexture: () => ({}), bindTexture: noop, texImage2D: noop, texParameteri: noop,
					createFramebuffer: () => ({}), bindFramebuffer: noop,
					createRenderbuffer: () => ({}), bindRenderbuffer: noop, renderbufferStorage: noop,
					framebufferTexture2D: noop, framebufferRenderbuffer: noop,
					createShader: () => ({}), shaderSource: noop, compileShader: noop,
					createProgram: () => ({}), attachShader: noop, linkProgram: noop,
					useProgram: noop, getProgramParameter: () => true, getShaderParameter: () => true,
					getShaderInfoLog: () => '', getProgramInfoLog: () => '',
					getUniformLocation: () => ({}), uniform1i: noop, uniform1f: noop, uniform2f: noop,
					uniform3f: noop, uniform4f: noop, uniformMatrix4fv: noop,
					enableVertexAttribArray: noop, vertexAttribPointer: noop,
					drawArrays: noop, drawElements: noop, viewport: noop,
					clear: noop, clearColor: noop, enable: noop, disable: noop,
					blendFunc: noop, depthFunc: noop, cullFace: noop,
					readPixels: function(x, y, w, h, fmt, type, out) {
						// Zero-filled is what a cleared buffer returns.
						if (out && out.fill) out.fill(0);
					},
					isContextLost: () => false,
					getError: () => 0,
					getContextAttributes: function() {
						return { alpha: true, antialias: true, depth: true,
							desynchronized: false, failIfMajorPerformanceCaveat: false,
							powerPreference: 'default', premultipliedAlpha: true,
							preserveDrawingBuffer: false, stencil: false,
							xrCompatible: false };
					},
					// WebGL2 additions (context requested as 'webgl2')
					createVertexArray: () => ({}), bindVertexArray: noop, deleteVertexArray: noop,
					fenceSync: () => ({}), clientWaitSync: () => 37147, deleteSync: noop
				};
				Object.keys(methods).forEach(function(name) {
					Object.defineProperty(glProto, name, {
						value: methods[name], writable: true, configurable: true, enumerable: false
					});
					disguise(methods[name], 'function ' + name + '() { [native code] }');
				});
				Object.setPrototypeOf(fake, glProto);
				// 'canvas' is an own getter on real contexts.
				try {
					defineNativeGetter(fake, 'canvas', function() { return canvas; });
				} catch (e) {}
				return fake;
			}

			function patchCanvasGetContext() {
				// NOTE on the two getParameter layers: the Phase 3.4 override
				// on WebGLRenderingContext.prototype.getParameter (main stealth
				// script) pins REAL contexts. The fake's proto chain also
				// ends at the native prototype, but its own glProto-level
				// getParameter SHADOWS that override — so the fake is pinned
				// by its own method, not by Phase 3.4. The layers are
				// complementary: real context → proto override, fake →
				// glProto method. Never assume the fake is "covered" by the
				// proto override.
				const orig = HTMLCanvasElement.prototype.getContext;
				HTMLCanvasElement.prototype.getContext = function(type, attrs) {
					if (type === 'webgl' || type === 'experimental-webgl' || type === 'webgl2') {
						const real = orig.call(this, type, attrs);
						if (real) {
							// Real context exists (GPU available): only pin the
							// fingerprint via the getParameter override already
							// installed below — do not replace it.
							return real;
						}
						if (!cachedFakeGL[type] || cachedFakeGL[type].canvas !== this) {
							cachedFakeGL[type] = buildFakeWebGL(this, type);
						}
						return cachedFakeGL[type];
					}
					return orig.call(this, type, attrs);
				};
				disguise(HTMLCanvasElement.prototype.getContext, 'function getContext() { [native code] }');
			}

			try { patchCanvasGetContext(); } catch (e) {}

			// ---- Stage 4: honest PluginArray ----
			// The old override replaced navigator.plugins with a plain ARRAY
			// — sannysoft's "Plugins is of type PluginArray: failed" catches
			// exactly that (Object.prototype.toString.call(plugins) must be
			// '[object PluginArray]'). Build real Plugin/MimeType instances
			// via Object.create on the native prototypes.
			function buildPlugins() {
				try {
					const pluginData = [
						{ name: 'Chrome PDF Plugin', description: 'Portable Document Format', filename: 'internal-pdf-viewer',
							mimes: [{ type: 'application/x-google-chrome-pdf', suffixes: 'pdf', description: 'Portable Document Format' }] },
						{ name: 'Chrome PDF Viewer', description: '', filename: 'mhjfbmdgcfjbbpaeojofohoefgiehjai',
							mimes: [{ type: 'application/x-google-chrome-viewer', suffixes: '', description: '' }] },
						{ name: 'Native Client', description: '', filename: 'internal-nacl-plugin',
							mimes: [{ type: 'application/x-nacl', suffixes: '', description: 'Native Client Executable' }] }
					];
					// Native-shape storage (#101 review warning 2): real Chrome
					// serves length/item/namedItem and the member fields as
					// PROTOTYPE getters/methods — own data props are what
					// fp-collect's descriptor walk flags. Values live in
					// WeakMaps so the getters can stay on the SHARED native
					// prototypes and the instance proto chain remains
					// exactly plugin.__proto__ === Plugin.prototype.
					const pluginVals = new WeakMap();   // Plugin -> data
					const pluginArrVals = new WeakMap(); // PluginArray -> [Plugin]
					const mimeVals = new WeakMap();     // MimeType -> data
					const mimeArrVals = new WeakMap();  // MimeTypeArray -> [MimeType]

					function protoGetter(proto, prop, read) {
						const fn = function() { return read(this); };
						Object.defineProperty(proto, prop, {
							get: fn, set: undefined, enumerable: false, configurable: true
						});
						disguise(fn, 'function get ' + prop + '() { [native code] }');
					}
					function protoMethod(proto, prop, fn) {
						Object.defineProperty(proto, prop, {
							value: fn, writable: true, enumerable: false, configurable: true
						});
						disguise(fn, 'function ' + prop + '() { [native code] }');
					}

					// Plugin.prototype: name/description/filename/length.
					['name', 'description', 'filename'].forEach(function(prop) {
						protoGetter(Plugin.prototype, prop, function(self) {
							const v = pluginVals.get(self); return v ? v[prop] : undefined;
						});
					});
					protoGetter(Plugin.prototype, 'length', function(self) {
						const v = pluginVals.get(self); return v ? v.mimes.length : 0;
					});
					// PluginArray.prototype: length/item/namedItem.
					protoGetter(PluginArray.prototype, 'length', function(self) {
						const v = pluginArrVals.get(self); return v ? v.length : 0;
					});
					protoMethod(PluginArray.prototype, 'item', function(i) { return this[i] || null; });
					protoMethod(PluginArray.prototype, 'namedItem', function(n) { return this[n] || null; });
					// MimeType.prototype: type/suffixes/description/enabledPlugin.
					['type', 'suffixes', 'description', 'enabledPlugin'].forEach(function(prop) {
						protoGetter(MimeType.prototype, prop, function(self) {
							const v = mimeVals.get(self); return v ? v[prop] : undefined;
						});
					});
					// MimeTypeArray.prototype: length/item/namedItem.
					protoGetter(MimeTypeArray.prototype, 'length', function(self) {
						const v = mimeArrVals.get(self); return v ? v.length : 0;
					});
					protoMethod(MimeTypeArray.prototype, 'item', function(i) { return this[i] || null; });
					protoMethod(MimeTypeArray.prototype, 'namedItem', function(n) { return this[n] || null; });

					const plugins = Object.create(PluginArray.prototype);
					const mimeTypes = Object.create(MimeTypeArray.prototype);
					const mimeList = [];
					pluginData.forEach(function(p, i) {
						const plugin = Object.create(Plugin.prototype);
						pluginVals.set(plugin, p);
						const pluginMimes = [];
						p.mimes.forEach(function(m, j) {
							const mt = Object.create(MimeType.prototype);
							mimeVals.set(mt, { type: m.type, suffixes: m.suffixes,
								description: m.description, enabledPlugin: plugin });
							pluginMimes.push(mt);
							mimeList.push(mt);
							// Indexed own props ARE own on a real Plugin.
							Object.defineProperty(plugin, String(j), { value: mt, enumerable: true, configurable: true });
						});
						plugins[i] = plugin;
						// named access: plugins['Chrome PDF Plugin'] — also an
						// own prop in real Chrome (non-enumerable).
						Object.defineProperty(plugins, p.name, { value: plugin, enumerable: false, configurable: true });
					});
					mimeList.forEach(function(mt, i) { mimeTypes[i] = mt; });
					pluginArrVals.set(plugins, pluginData.map(function(_, i) { return plugins[i]; }));
					mimeArrVals.set(mimeTypes, mimeList);
					return { plugins: plugins, mimeTypes: mimeTypes };
				} catch (e) {
					return null;
				}
			}

			const built = buildPlugins();
			if (built) {
				try { delete navigator.plugins; } catch (e) {}
				Object.defineProperty(Navigator.prototype, 'plugins', {
					get: function() { return built.plugins; },
					set: undefined,
					configurable: true,
					enumerable: true
				});
				try { delete navigator.mimeTypes; } catch (e) {}
				Object.defineProperty(Navigator.prototype, 'mimeTypes', {
					get: function() { return built.mimeTypes; },
					set: undefined,
					configurable: true,
					enumerable: true
				});
			}

			// ---- Stage 5: window.chrome mock ----
			// Ground truth (raw headless Chromium 149 probe): native
			// window.chrome = { loadTimes, csi, app } with NO runtime and
			// webstore === undefined. fp-collect's detailChrome calls
			// chrome.runtime.connect — on headless (real shape) that member
			// simply does not exist, which is NOT a tell; the tell was our
			// old mock exposing runtime: {} (connect === undefined reads
			// "runtime present but broken" = extension-less automation).
			//
			// Review M1 (PR #104): the ENGINE overwrites any 'app' member
			// we set on the mock with its own native 7-prop app
			// (installState/runningState included) — the mock's app was
			// dead code and a latent tell. So we deliberately do NOT mock
			// 'app': the engine's native one is strictly better. Only
			// csi/loadTimes are mocked (the engine leaves those alone).
			//
			// Review M2: csi/loadTimes must NOT carry a per-function own
			// toString (native functions have exactly [length, name,
			// prototype]) — register them in the WeakMap only; the GLOBAL
			// Function.prototype.toString interceptor serves the mask.
			function registerDisguise(fn, nativeText) {
				try { disguised.set(fn, nativeText); } catch (e) {}
			}
			(function patchWindowChrome() {
				const csi = function() {
					return { startE: Date.now(), onloadT: Date.now(),
						pageT: Date.now() %% 100000, tran: 15 };
				};
				const loadTimes = function() {
					const now = Date.now() / 1000;
					return {
						requestTime: now, startLoadTime: now,
						commitLoadTime: now, finishDocumentLoadTime: now,
						finishLoadTime: now, firstPaintTime: 0,
						firstPaintAfterLoadTime: 0, navigationType: 'Other',
						wasFetchedViaSpdy: false, wasNpnNegotiated: false,
						npnNegotiatedProtocol: '',
						wasAlternateProtocolAvailable: false,
						connectionInfo: 'unknown'
					};
				};
				const chromeMock = {
					csi: csi,
					loadTimes: loadTimes
				};
				// WeakMap-only registration: String(fn) still reads native
				// via the global FTS interceptor, and getOwnPropertyNames(fn)
				// stays [length, name, prototype] like a native function.
				registerDisguise(csi, 'function csi() { [native code] }');
				registerDisguise(loadTimes, 'function loadTimes() { [native code] }');
				// 'app' is intentionally absent — see the M1 note above.
				// Native descriptor shape: own data prop, writable,
				// enumerable, NON-configurable.
				Object.defineProperty(window, 'chrome', {
					value: chromeMock, writable: true, enumerable: true, configurable: false
				});
			})();

			// (disguise is defined at the top of this IIFE; the plugins
			// getter is registered there.)
			const protoPluginsDesc = Object.getOwnPropertyDescriptor(Navigator.prototype, 'plugins');
			if (protoPluginsDesc && protoPluginsDesc.get) {
				disguise(protoPluginsDesc.get, 'function get plugins() { [native code] }');
			}
		})()
	`, profile.WebGLVendor, profile.WebGLRenderer)
}
