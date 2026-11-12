// lib/tryon/prompts.ts — 六阶段换装管线的冻结提示词（v1 商品图 / v2 人物标准化 / v4 最终换装）。
// 英文生成提示词保持文档原文，历史任务按创建时冻结版本继续可追溯。

/** 服装识别：系统提示词（中文，强制只返回结构化 JSON）。 */
export const GARMENT_RECOGNITION_SYSTEM_PROMPT = '你是服装识别器。只识别图片中的服装，严格按 JSON schema 返回，不解释。';

/**
 * 服装识别：用户提示词模板。
 * 占位符：{category} 粗分类、{width}/{height} 原图像素尺寸。
 */
export const GARMENT_RECOGNITION_USER_TEMPLATE =
  '目标类别是 {category}。返回所有候选服装的像素 bbox=[left,top,right,bottom]。' +
  'garment_type 使用具体、通用的服装名称，例如 T-shirt、shirt、hoodie、sweater、jacket、coat、jeans、skirt、sneakers。' +
  'layer_hint 仅可为 inner、middle、outer、one_piece、lower、footwear 或 unknown；' +
  'T-shirt/衬衫是 inner，毛衣/卫衣是 middle，夹克/外套/棉袄是 outer。' +
  '尽可能描述可见的材质、纹理、图案、版型、长度、袖型、领型、闭合方式和辨识性细节，不确定的字段返回空值。' +
  'bottom 不得把 socks 当作下装；左右鞋均标记为 shoes。' +
  '输入原图宽 {width} 像素、高 {height} 像素。' +
  'bbox 必须使用原图像素坐标，满足 0<=left<right<={width} 且 0<=top<bottom<={height}；不要使用内部缩放坐标。';

/**
 * 单件商品图 v1。Image 1 是裁剪目标，Image 2 是权威原图。
 * 占位符：{garment_type} 具体服装名、{hints} 归一化语义画像 JSON。
 */
export const PRODUCT_IMAGE_V1_PROMPT =
  'Create one clean product reference of the exact {garment_type} shown in Image 1. Image 1 is the cropped target and Image 2 is the authoritative original upload. ' +
  'Preserve its silhouette, material, colors, patterns, logos, seams, closures and asymmetric details. ' +
  'Show only the complete item centered on a plain white background; no person, mannequin, hanger, text or extra object. ' +
  'The structured garment profile is authoritative when it describes material, layering or construction details: {hints}.';

/** 人物标准化 v2：身份保持的正面全身工作室参考图（强制姿态/构图/背景归一化）。 */
export const PERSONA_V2_PROMPT =
  'Transform Image 1 into one identity-preserving, front-view full-body studio reference. This is a mandatory pose,framing and background normalization task: do not return or closely copy the source composition. ' +
  'Preserve the exact person, face, apparent age, facial proportions, skin tone, hairstyle, hair color and natural body build. ' +
  'Do not beautify, slim, reshape, age, change gender presentation or alter identity. Preserve every visible garment,shoe and accessory exactly; do not restyle, replace, remove or invent clothing. ' +
  'Always output the person standing upright from the top of the hair through both complete shoe soles, centered in a 9:16 portrait with comfortable margin above the head and below the feet. ' +
  'Normalize any crouching, sitting, leaning,twisted, side-view, action or off-center source into a neutral catalog stance: head level, face, gaze and torso towardthe camera, shoulders even, arms naturally lowered, hands visible and separated from the torso, legs straight andboth feet grounded. Reconstruct only occluded or out-of-frame anatomy and clothing needed for this complete stance. ' +
  'Use a seamless plain light-gray studio background, eye-level camera, bright even lighting and a natural neutralexpression with both eyes open and lips gently closed. ' +
  'Before output, verify silently: one person only; recognizable unchanged identity; upright frontal pose; completehead, hands, legs and shoes; unchanged clothing; no copied scenery; no props, text, border, collage, duplicate limb,cropped body part, dramatic shadow or face drift. Output exactly one photorealistic image.';

/**
 * 最终换装 v4：固定三图 [人物参考, 服装板, 姿势参考]。
 * 占位符：{item_count} 实际服装数、{garment_manifest} 与格位同序的 JSON 清单。
 */
export const TRYON_V4_PROMPT =
  'Create one photorealistic virtual try-on image. Image 1 is the authoritative front-view full-body person;' +
  'Image 2 is a 3x2 product board containing every garment and wearable item to put on that same person;' +
  'Image 3 is a full-body pose reference that only sets the body pose, not identity or clothing. ' +
  "Preserve Image 1's exact identity, face, age, skin tone, hairstyle, body build, proportions, expression, camera,subject scale and complete head-to-shoe framing. Transfer Image 3's exact pose onto Image 1's person: adopt itsfull-body stance, arm and leg positions, hand placement, torso orientation, head turn and weight distribution, whilekeeping Image 1's identity, proportions and framing. Replace only clothing and wearable accessories. " +
  "Use every non-empty, non-duplicate item from Image 2 exactly once. Preserve each item's type,silhouette, fit, length, layering, exact colors, material, texture, patterns, logos, seams, closures, trim,hardware and asymmetric details. " +
  'Place items in anatomically correct positions and a physically plausible layerorder. Do not merge separate items, redesign clothing, invent absent items or copy the board background. ' +
  'Keep the same bright neutral studio presentation. No extra person, duplicate garment, altered anatomy, pose orface drift, beauty retouching, caption, border or collage. Output exactly one clean front-view full-body image. ' +
  'The board has exactly {item_count} occupied product cells, and all {item_count} items are mandatory in thefinished outfit. First inventory every occupied cell, then dress the person with the complete inventory; do notsilently omit or choose between items. ' +
  'A jacket, blazer, coat, cardigan or hoodie is outerwear, not a substitutefor an inner top. When both outerwear and a T-shirt, shirt or other inner top are present, wear both: keep the innertop underneath and the outerwear visibly layered over it, open at the front when needed to show both garments. ' +
  'Before output, silently verify that the number of worn board items is exactly {item_count}. ' +
  'Product fidelity has higher priority than beautification, styling or making an item look more conventional. Treateach occupied cell as an authoritative product photograph, never as loose inspiration. Preserve the exact materialidentity and its visible physical properties: surface grain, gloss or matte finish, specular highlights, thickness,stiffness, drape and characteristic wrinkles. ' +
  'Never convert leather into nylon, cotton or another fabric, and neversimplify distinctive panels, zippers, pockets or hardware. Adapt only the item\'s pose and natural folds required tofit the person; if a detail is uncertain, copy the reference instead of inventing or substituting it. ' +
  'The following JSON manifest describes Image 2 cells in reading order: {garment_manifest}. Use garment_type,layer_hint, material, texture and construction hints to disambiguate visually similar items and build a physicallycorrect layer order. ' +
  'inner is worn closest to the body, middle above inner, and outer above both. When severalitems compete for the same body position and cannot physically be worn together, treat them as alternatives andchoose the most coherent option. ' +
  'This alternatives rule supersedes the earlier mandatory-item rule only for thosephysically competing items; never omit compatible inner/middle/outer layers. Do not invent details that are absentfrom both the product cell and its manifest.';

/** 当前管线版本：写入 outfit_generations.pipeline_version，便于后续提示词演进时区分。 */
export const PIPELINE_VERSION = 'tryon-v1';
