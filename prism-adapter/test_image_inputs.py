import base64
import unittest
from image_inputs import ImageReferenceError, digest, resolve

class ImageInputTests(unittest.TestCase):
    def test_data_url_is_bounded_and_digestible(self):
        raw, mime = resolve('data:image/png;base64,' + base64.b64encode(b'fixture').decode())
        self.assertEqual((raw, mime), (b'fixture', 'image/png'))
        self.assertEqual(len(digest(raw)), 64)

    def test_private_http_is_rejected_before_fetch(self):
        with self.assertRaisesRegex(ImageReferenceError, 'public'):
            resolve('http://127.0.0.1/x.png')
