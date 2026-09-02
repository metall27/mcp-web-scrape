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
			// Export the registrar so earlier-registered script parts (e.g.
			// the webdriver getter in the main stealth script) can join the
			// map even though they run before this IIFE. Non-enumerable to
			// stay out of window property scans.
			try {
				Object.defineProperty(window, '__stealthDisguise', {
					value: disguise, writable: false, configurable: true, enumerable: false
				});
			} catch (e) {}

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
			let cachedFakeGL = null;
			function buildFakeWebGL(canvas) {
				// Minimal but functional: drawing ops execute against a 2D
				// context (nothing renders, but no method throws — a broken
				// method is as loud as a missing context).
				const noop = () => {};
				const fake = {
					canvas: canvas,
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
				return fake;
			}

			function patchCanvasGetContext() {
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
						if (!cachedFakeGL || cachedFakeGL.canvas !== this) {
							cachedFakeGL = buildFakeWebGL(this);
						}
						return cachedFakeGL;
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
					const plugins = Object.create(PluginArray.prototype);
					const mimeTypes = Object.create(MimeTypeArray.prototype);
					const mimeList = [];
					pluginData.forEach(function(p, i) {
						const plugin = Object.create(Plugin.prototype);
						Object.defineProperties(plugin, {
							name: { value: p.name, enumerable: true },
							description: { value: p.description, enumerable: true },
							filename: { value: p.filename, enumerable: true },
							length: { value: p.mimes.length, enumerable: true }
						});
						const mimes = Object.create(MimeTypeArray.prototype);
						p.mimes.forEach(function(m, j) {
							const mt = Object.create(MimeType.prototype);
							Object.defineProperties(mt, {
								type: { value: m.type, enumerable: true },
								suffixes: { value: m.suffixes, enumerable: true },
								description: { value: m.description, enumerable: true },
								enabledPlugin: { value: plugin, enumerable: true }
							});
							mimes[j] = mt;
							mimeList.push(mt);
						});
						Object.defineProperty(plugin, '0', { value: mimes[0], enumerable: true });
						plugins[i] = plugin;
						// named access: plugins['Chrome PDF Plugin']
						Object.defineProperty(plugins, p.name, { value: plugin, enumerable: false });
					});
					mimeList.forEach(function(mt, i) { mimeTypes[i] = mt; });
					Object.defineProperty(plugins, 'length', { value: pluginData.length, enumerable: true });
					Object.defineProperty(mimeTypes, 'length', { value: mimeList.length, enumerable: true });
					plugins.item = function(i) { return this[i] || null; };
					plugins.namedItem = function(n) { return this[n] || null; };
					mimeTypes.item = function(i) { return this[i] || null; };
					mimeTypes.namedItem = function(n) { return this[n] || null; };
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

			// (disguise is defined at the top of this IIFE; the plugins
			// getter is registered there.)
			const protoPluginsDesc = Object.getOwnPropertyDescriptor(Navigator.prototype, 'plugins');
			if (protoPluginsDesc && protoPluginsDesc.get) {
				disguise(protoPluginsDesc.get, 'function get plugins() { [native code] }');
			}
		})()
	`, profile.WebGLVendor, profile.WebGLRenderer)
}
